//go:build integration

package edgelink

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ports are the host ports the compose file publishes; the environment overrides both sides.
type ports struct {
	redis, connector, edgeLinkHTTP, edgeLinkMetrics, toxiproxyAPI, proxy, liveData int
}

func portsFromEnv() ports {
	return ports{
		redis:           envInt("ZQNT_TEST_REDIS_PORT", 26379),
		connector:       envInt("ZQNT_TEST_CONNECTOR_PORT", 28010),
		edgeLinkHTTP:    envInt("ZQNT_TEST_EDGE_LINK_HTTP_PORT", 28007),
		edgeLinkMetrics: envInt("ZQNT_TEST_EDGE_LINK_METRICS_PORT", 28009),
		toxiproxyAPI:    envInt("ZQNT_TEST_TOXIPROXY_PORT", 28474),
		proxy:           envInt("ZQNT_TEST_PROXY_PORT", 28006),
		liveData:        envInt("ZQNT_TEST_LIVE_DATA_PORT", 28003),
	}
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// stack is the compose project of one test run with the keys it minted.
type stack struct {
	t            *testing.T
	project      string
	composeFile  string
	ports        ports
	installation string
	organization string
	serviceKey   ed25519.PrivateKey
	licenseKey   ed25519.PrivateKey
	certDir      string
	env          []string
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose is not available")
	}
}

// startStack brings up the platform side, publishes a license lease for the test organization
// and waits until connector and edge-link answer their readiness checks.
func startStack(t *testing.T) *stack {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s := &stack{
		t:            t,
		project:      "edgelink-tox-" + uuid.NewString()[:8],
		composeFile:  filepath.Join(dir, "docker-compose.yml"),
		ports:        portsFromEnv(),
		installation: "edge-link-toxiproxy",
		organization: uuid.NewString(),
	}
	if _, s.serviceKey, err = ed25519.GenerateKey(rand.Reader); err != nil {
		t.Fatal(err)
	}
	if _, s.licenseKey, err = ed25519.GenerateKey(rand.Reader); err != nil {
		t.Fatal(err)
	}
	s.certDir = writeServerCertificate(t)
	liveData := buildLiveData(t)
	anchor, err := newAnchorKey()
	if err != nil {
		t.Fatal(err)
	}
	s.env = append(os.Environ(),
		"ZQNT_TEST_INSTALLATION_ID="+s.installation,
		"ZQNT_TEST_LICENSE_PUBLIC_KEY="+spki(t, s.licenseKey.Public()),
		"ZQNT_TEST_SERVICE_PRIVATE_KEY="+pkcs8(t, s.serviceKey),
		"ZQNT_TEST_SERVICE_PUBLIC_KEY="+spki(t, s.serviceKey.Public()),
		"ZQNT_TEST_ANCHOR_PRIVATE_KEY="+anchor,
		"ZQNT_TEST_CERT_DIR="+s.certDir,
		"ZQNT_TEST_LIVE_DATA_BIN="+liveData,
	)
	t.Cleanup(s.stop)

	s.compose("up", "-d", "--quiet-pull", "postgres", "redis", "toxiproxy", "live-data")
	s.publishLease()
	s.compose("up", "-d", "--quiet-pull", "connector", "edge-link")
	waitFor(t, "connector ready", 3*time.Minute, func() bool {
		return httpOK(fmt.Sprintf("http://127.0.0.1:%d/q/health/ready", s.ports.connector))
	})
	waitFor(t, "edge-link ready", 2*time.Minute, func() bool {
		return httpOK(fmt.Sprintf("http://127.0.0.1:%d/q/health/ready", s.ports.edgeLinkHTTP))
	})
	return s
}

func (s *stack) compose(args ...string) {
	s.t.Helper()
	out, err := s.composeOutput(args...)
	if err != nil {
		s.t.Fatalf("docker compose %v: %v\n%s", args, err, out)
	}
}

func (s *stack) composeOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", append([]string{"compose", "-f", s.composeFile, "-p", s.project}, args...)...)
	cmd.Env = s.env
	return cmd.CombinedOutput()
}

// stop keeps the service logs next to the test's log directory and removes the project; with
// ZQNT_TEST_KEEP_STACK set the project stays up for a look.
func (s *stack) stop() {
	if logs, err := s.composeOutput("logs", "--no-color", "--timestamps"); err == nil {
		s.keepLog("platform.log", logs)
	}
	if os.Getenv("ZQNT_TEST_KEEP_STACK") != "" {
		s.t.Logf("stack kept: docker compose -p %s", s.project)
		return
	}
	if out, err := s.composeOutput("down", "-v", "--remove-orphans", "-t", "5"); err != nil {
		s.t.Logf("docker compose down: %v\n%s", err, out)
	}
}

// keepLog writes a log of this run into ZQNT_TEST_LOG_DIR when set.
func (s *stack) keepLog(name string, content []byte) {
	dir := os.Getenv("ZQNT_TEST_LOG_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		err = os.WriteFile(filepath.Join(dir, name), content, 0o644)
		if err != nil {
			s.t.Logf("keep %s: %v", name, err)
		}
	}
}

func (s *stack) publishLease() {
	s.t.Helper()
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", s.ports.redis)})
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	waitFor(s.t, "redis", 30*time.Second, func() bool { return client.Ping(ctx).Err() == nil })
	lease := mintLease(s.t, s.licenseKey, s.installation, s.organization)
	if err := client.Set(ctx, "zqnt:license:lease:"+s.organization, lease, 0).Err(); err != nil {
		s.t.Fatalf("publish lease: %v", err)
	}
}

// mintLease signs a license lease as the hub would: an EdDSA compact JWS naming the installation
// and the organization, every feature, valid for an hour.
func mintLease(t *testing.T, key ed25519.PrivateKey, installation, organization string) string {
	t.Helper()
	now := time.Now()
	return signJWS(t, key, map[string]any{
		"iss":               "https://api.zequent.com",
		"aud":               []string{"zqnt-framework"},
		"jti":               uuid.NewString(),
		"license_id":        "lic-" + uuid.NewString()[:8],
		"activation_id":     "act-" + uuid.NewString()[:8],
		"installation_id":   installation,
		"organization_id":   organization,
		"organization_name": "Edge Link Toxiproxy Test",
		"product":           "zqnt-framework",
		"features":          []string{"*"},
		"limits":            map[string]int64{},
		"iat":               now.Unix(),
		"nbf":               now.Add(-time.Minute).Unix(),
		"exp":               now.Add(time.Hour).Unix(),
		"grace_until":       now.Add(2 * time.Hour).Unix(),
	})
}

func signJWS(t *testing.T, key ed25519.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(header) + "." + enc.EncodeToString(payload)
	return input + "." + enc.EncodeToString(ed25519.Sign(key, []byte(input)))
}

func spki(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func pkcs8(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// newAnchorKey is edge-link's installation anchor: a P-256 key in PKCS#8, base64.
func newAnchorKey() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// writeServerCertificate makes edge-link's self-signed P-256 certificate for the name edge-link,
// readable by the container's user, and returns the directory holding it.
func writeServerCertificate(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "edge-link-tls-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "edge-link"},
		DNSNames:              []string{"edge-link", "localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	for name, content := range map[string][]byte{"edge-link.crt": certPEM, "edge-link.key": keyPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func (s *stack) certFile() string { return filepath.Join(s.certDir, "edge-link.crt") }

// buildLiveData compiles the live-data stand-in as a static Linux binary for its container,
// readable by any user.
func buildLiveData(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "edge-link-live-data-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "live-data")
	cmd := exec.Command("go", "build", "-o", bin, "./livedata")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build live-data stand-in: %v\n%s", err, out)
	}
	return bin
}

func httpOK(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitFor(t *testing.T, what string, timeout time.Duration, ready func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	deadline := start.Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not ready after %s", what, timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return time.Since(start)
}

// toxiproxy drives one proxy over toxiproxy's HTTP API.
type toxiproxy struct {
	t    *testing.T
	base string
	name string
}

func (s *stack) toxiproxy(name, listen, upstream string) *toxiproxy {
	s.t.Helper()
	p := &toxiproxy{t: s.t, base: fmt.Sprintf("http://127.0.0.1:%d", s.ports.toxiproxyAPI), name: name}
	waitFor(s.t, "toxiproxy", time.Minute, func() bool { return httpOK(p.base + "/version") })
	p.call(http.MethodPost, "/proxies", map[string]any{"name": name, "listen": listen, "upstream": upstream, "enabled": true})
	return p
}

func (p *toxiproxy) enable(on bool) {
	p.t.Helper()
	p.call(http.MethodPost, "/proxies/"+p.name, map[string]any{"enabled": on})
}

func (p *toxiproxy) addToxic(name, kind, stream string, attributes map[string]any) {
	p.t.Helper()
	p.call(http.MethodPost, "/proxies/"+p.name+"/toxics", map[string]any{
		"name": name, "type": kind, "stream": stream, "toxicity": 1.0, "attributes": attributes,
	})
}

func (p *toxiproxy) removeToxic(name string) {
	p.t.Helper()
	p.call(http.MethodDelete, "/proxies/"+p.name+"/toxics/"+name, nil)
}

func (p *toxiproxy) call(method, path string, body map[string]any) {
	p.t.Helper()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			p.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, p.base+path, bytes.NewReader(payload))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		p.t.Fatalf("toxiproxy %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var detail bytes.Buffer
		_, _ = detail.ReadFrom(resp.Body)
		p.t.Fatalf("toxiproxy %s %s: %s: %s", method, path, resp.Status, detail.String())
	}
}

func httpGet(url string) (string, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}
