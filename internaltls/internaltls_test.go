package internaltls_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/internaltls"
	"github.com/zequent/zqnt-sdk-go/v3/internaltls/internaltlstest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func lookup(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestNothingSetIsOff(t *testing.T) {
	c := internaltls.FromLookup(lookup(nil))
	if c.Enabled || c.CAFile != internaltls.DefaultCAFile {
		t.Fatalf("config = %+v", c)
	}
	creds, err := c.TransportCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Errorf("off must dial plaintext, got %s", creds.Info().SecurityProtocol)
	}
	if cfg, err := c.TLSConfig(); cfg != nil || err != nil {
		t.Errorf("TLSConfig off = %v, %v", cfg, err)
	}
	client, err := c.HTTPClient(time.Second)
	if err != nil || client.Transport != nil {
		t.Errorf("off is a plain client: %v %v", client, err)
	}
}

func TestTheSwitchAndTheCAFileAreRead(t *testing.T) {
	for _, raw := range []string{"true", " TRUE ", "1", "yes", "On"} {
		c := internaltls.FromLookup(lookup(map[string]string{"ZQNT_INTERNAL_TLS": raw, "ZQNT_INTERNAL_TLS_CA": " /ca/x.crt "}))
		if !c.Enabled || c.CAFile != "/ca/x.crt" {
			t.Errorf("%q: config = %+v", raw, c)
		}
	}
	for _, raw := range []string{"false", "", "maybe", "0"} {
		if internaltls.FromLookup(lookup(map[string]string{"ZQNT_INTERNAL_TLS": raw})).Enabled {
			t.Errorf("%q must be off", raw)
		}
	}
}

func TestAMissingOrEmptyCAIsAnError(t *testing.T) {
	missing := internaltls.Config{Enabled: true, CAFile: filepath.Join(t.TempDir(), "none.crt")}
	for name, f := range map[string]func() error{
		"TransportCredentials": func() error { _, err := missing.TransportCredentials(); return err },
		"TLSConfig":            func() error { _, err := missing.TLSConfig(); return err },
		"HTTPClient":           func() error { _, err := missing.HTTPClient(time.Second); return err },
		"CertPool":             func() error { _, err := missing.CertPool(); return err },
	} {
		err := f()
		if err == nil || !strings.Contains(err.Error(), "ZQNT_INTERNAL_TLS_CA") {
			t.Errorf("%s: want an error naming the variable, got %v", name, err)
		}
	}
	empty := filepath.Join(t.TempDir(), "empty.crt")
	if err := os.WriteFile(empty, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (internaltls.Config{Enabled: true, CAFile: empty}).TLSConfig(); err == nil || !strings.Contains(err.Error(), "no PEM certificate") {
		t.Errorf("want the no-certificate error, got %v", err)
	}
}

func serveHealthTLS(t *testing.T, server *tls.Config) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(server)))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func check(addr string, creds credentials.TransportCredentials) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func TestAConnectionVerifiesTheServerAgainstTheCA(t *testing.T) {
	pki := internaltlstest.NewTestCertificate(t, t.TempDir())
	addr := serveHealthTLS(t, pki.Server)

	creds, err := internaltls.Config{Enabled: true, CAFile: pki.CAFile}.TransportCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if err := check(addr, creds); err != nil {
		t.Fatalf("over TLS with the CA: %v", err)
	}

	other := internaltlstest.NewTestCertificate(t, t.TempDir())
	wrong, err := internaltls.Config{Enabled: true, CAFile: other.CAFile}.TransportCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if err := check(addr, wrong); err == nil {
		t.Fatal("another CA must not verify the server")
	}
	plain, _ := internaltls.Config{}.TransportCredentials()
	if err := check(addr, plain); err == nil {
		t.Fatal("plaintext must not reach a TLS server")
	}
}

func TestTheHTTPClientTrustsTheCANextToTheSystemRoots(t *testing.T) {
	pki := internaltlstest.NewTestCertificate(t, t.TempDir())
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = pki.Server
	server.StartTLS()
	t.Cleanup(server.Close)

	client, err := internaltls.Config{Enabled: true, CAFile: pki.CAFile}.HTTPClient(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("GET over the CA: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	plain, _ := internaltls.Config{}.HTTPClient(5 * time.Second)
	if _, err := plain.Get(server.URL); err == nil {
		t.Fatal("without the CA the server is not trusted")
	}
}
