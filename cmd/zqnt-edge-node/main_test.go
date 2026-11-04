package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/identitytest"
)

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestFlags(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("identity: {key_store: software-dev, dev: true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	example, err := os.ReadFile(filepath.Join("..", "..", "examples", "edge-node", "node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	noTPM := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(noTPM, []byte(strings.Replace(string(example), "  # tpm_device: /dev/tpmrm0",
		"  tpm_device: "+filepath.Join(t.TempDir(), "tpmrm0"), 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	devExample, err := os.ReadFile(filepath.Join("..", "..", "examples", "edge-node", "node-dev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sealingKey := filepath.Join(t.TempDir(), "sealing.key")
	if err := os.WriteFile(sealingKey, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	exampleCfg := filepath.Join(t.TempDir(), "node-dev.yaml")
	adjusted := strings.ReplaceAll(string(devExample), "dir: ./identity", "dir: "+filepath.Join(t.TempDir(), "identity"))
	adjusted = strings.ReplaceAll(adjusted, "# sealing_key_file: ./dev-sealing.key", "sealing_key_file: "+sealingKey)
	if err := os.WriteFile(exampleCfg, []byte(adjusted), 0o600); err != nil {
		t.Fatal(err)
	}
	missingKey := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(missingKey, []byte(strings.ReplaceAll(adjusted, sealingKey, sealingKey+".missing")), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args        []string
		code        int
		out, errOut string
	}{
		{[]string{"--version"}, 0, "zqnt-edge-node dev", ""},
		{[]string{"--config", exampleCfg, "--check-config"}, 0, "configuration is valid; not enrolled yet", ""},
		{[]string{"--config", missingKey, "--check-config"}, 1, "", "sealing key"},
		{[]string{"--config", noTPM, "--check-config"}, 1, "", "identity.tpm_device"},
		{[]string{"--config", bad, "--check-config"}, 1, "", "links: one link is required"},
		{[]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}, 1, "", "no such file"},
		{[]string{"--bogus"}, 2, "", "flag provided but not defined"},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
		{[]string{"enroll", "--config", exampleCfg}, 2, "", "--claim-code is required"},
		{[]string{"identity", "--config", exampleCfg}, 1, "", "not enrolled"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			code, out, errOut := runCmd(t, c.args...)
			if code != c.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, c.code, out, errOut)
			}
			if !strings.Contains(out, c.out) || !strings.Contains(errOut, c.errOut) {
				t.Fatalf("stdout %q stderr %q", out, errOut)
			}
		})
	}
}

func TestEnrollRotateAndRenew(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	creds := identitytest.NewTLS(t)
	srv := identitytest.StartEnrollment(t, auth, &creds)
	dir := filepath.Join(t.TempDir(), "identity")
	cfg := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(cfg, fmt.Appendf(nil, `
identity: {key_store: software-dev, dev: true, dir: %q}
assets: [DOCK-1]
links:
  - id: lte
    driver: ip-session
    endpoint: %q
    ca_file: %q
    server_name: localhost
`, dir, srv.Addr, creds.CAFile), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCmd(t, "enroll", "--config", cfg, "--claim-code", srv.ClaimCode, "--display-name", "gate")
	if code != 0 {
		t.Fatalf("enroll: exit %d: %s", code, errOut)
	}
	nodes := srv.Nodes()
	if len(nodes) != 1 || !strings.Contains(out, "node id:             "+nodes[0].NodeID) ||
		!strings.Contains(out, fingerprint(auth.Anchor.ID)) || len(nodes[0].Assets) != 1 {
		t.Fatalf("enroll printed:\n%s\nplatform has %+v", out, nodes)
	}
	if code, _, errOut := runCmd(t, "enroll", "--config", cfg, "--claim-code", srv.ClaimCode); code != 1 || !strings.Contains(errOut, "already enrolled") {
		t.Fatalf("second enroll: exit %d: %s", code, errOut)
	}
	if code, out, _ := runCmd(t, "--config", cfg, "--check-config"); code != 0 || !strings.Contains(out, "enrolled as node "+nodes[0].NodeID) {
		t.Fatalf("check-config: %d %s", code, out)
	}
	if code, out, _ := runCmd(t, "identity", "--config", cfg); code != 0 || !strings.Contains(out, "credential expires") {
		t.Fatalf("identity: %d %s", code, out)
	}
	before := srv.Nodes()[0].KeyID
	if code, out, errOut := runCmd(t, "rotate-key", "--config", cfg); code != 0 || !strings.Contains(out, "identity key rotated") {
		t.Fatalf("rotate-key: %d %s %s", code, out, errOut)
	}
	if bytes.Equal(before, srv.Nodes()[0].KeyID) {
		t.Fatal("platform key unchanged after rotate-key")
	}
	oldCredential := srv.Nodes()[0].Credential
	if code, out, errOut := runCmd(t, "renew-credential", "--config", cfg); code != 0 || !strings.Contains(out, "credential renewed") {
		t.Fatalf("renew-credential: %d %s %s", code, out, errOut)
	}
	token, _, err := identity.Dir(dir).Credential()
	if err != nil || token == oldCredential || token != srv.Nodes()[0].Credential {
		t.Fatalf("credential after renewal %q (%v)", token, err)
	}
}

func TestEnrollWithAProvisioningFile(t *testing.T) {
	auth := identitytest.NewAuthority(t)
	creds := identitytest.NewTLS(t)
	srv := identitytest.StartEnrollment(t, auth, &creds)
	ca, err := os.ReadFile(creds.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "identity")
	cfg := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(cfg, fmt.Appendf(nil, `
identity: {key_store: software-dev, dev: true, dir: %q}
links: [{id: lte, driver: ip-session, endpoint: "unused:1"}]
`, dir), 0o600); err != nil {
		t.Fatal(err)
	}
	prov := identity.Provisioning{
		Version: 1, InstallationID: auth.InstallationID, Anchors: auth.Anchors(), ClaimCode: srv.ClaimCode,
		Platform: identity.ProvisioningPlatform{Endpoint: srv.Addr, ServerName: "localhost", CAPEM: string(ca)},
	}
	data, err := json.Marshal(prov)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "provisioning.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCmd(t, "enroll", "--config", cfg, "--provisioning", file); code != 0 || strings.Contains(out, "compare the anchor fingerprint") {
		t.Fatalf("enroll: %d\n%s\n%s", code, out, errOut)
	}

	other := identitytest.NewAuthority(t)
	prov.Anchors = other.Anchors()
	qr, err := prov.EncodeQR()
	if err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "enroll", "--config", cfg, "--reenroll", "--provisioning-qr", qr); code != 1 || !strings.Contains(errOut, "provisioning") {
		t.Fatalf("enroll against other anchors: %d %s", code, errOut)
	}
}
