package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlags(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("node_id: pi-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	example := filepath.Join("..", "..", "examples", "edge-node", "node.yaml")
	cases := []struct {
		args        []string
		code        int
		out, errOut string
	}{
		{[]string{"--version"}, 0, "zqnt-edge-node dev", ""},
		{[]string{"--config", example, "--check-config"}, 0, "configuration is valid", ""},
		{[]string{"--config", bad, "--check-config"}, 1, "", "links: one link is required"},
		{[]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}, 1, "", "no such file"},
		{[]string{"--bogus"}, 2, "", "flag provided but not defined"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(c.args, &out, &errOut); code != c.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, c.code, out.String(), errOut.String())
			}
			if !strings.Contains(out.String(), c.out) || !strings.Contains(errOut.String(), c.errOut) {
				t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
			}
		})
	}
}
