package tpmsim

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-tpm-tools/simulator"
	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
)

// open starts the simulator with a fixed seed, so a second start is the same TPM.
func open(t *testing.T, dir string) *identity.TPMKeyStore {
	t.Helper()
	sim, err := simulator.GetWithFixedSeedInsecure(7)
	if err != nil {
		t.Fatal(err)
	}
	s, err := identity.NewTPMKeyStore(dir, transport.FromReadWriteCloser(sim))
	if err != nil {
		_ = sim.Close()
		t.Fatal(err)
	}
	return s
}

func TestTPMKeyStore(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if s.Protection() != edgelinkv3.KeyProtection_KEY_PROTECTION_TPM {
		t.Fatalf("protection %v", s.Protection())
	}
	k, err := s.Generate(identity.KeyNext)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := identity.PublicKeyBytes(k)
	if err != nil {
		t.Fatal(err)
	}
	transcript := []byte("zqnt-edge-link-v1 hello test")
	sig, err := handshake.Sign(k, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake.Verify(handshake.AlgECDSAP256, pub, transcript, sig); err != nil {
		t.Fatalf("TPM signature: %v", err)
	}
	if err := s.Rename(identity.KeyNext, identity.KeyActive); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = open(t, dir)
	defer func() { _ = s.Close() }()
	loaded, err := s.Load(identity.KeyActive)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := identity.PublicKeyBytes(loaded)
	if !bytes.Equal(again, pub) {
		t.Fatal("reloaded key has another public key")
	}
	sig, err = handshake.Sign(loaded, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake.Verify(handshake.AlgECDSAP256, pub, transcript, sig); err != nil {
		t.Fatalf("signature of the reloaded key: %v", err)
	}

	state := &identity.State{IdentityKeyID: handshake.KeyID(handshake.AlgECDSAP256, pub), Suite: 0x0101}
	p, err := identity.NewProvider(identity.Dir(dir), s, state)
	if err != nil {
		t.Fatal(err)
	}
	if p.Protection() != "KEY_PROTECTION_TPM" {
		t.Fatalf("provider protection %s", p.Protection())
	}

	if err := s.Delete(identity.KeyActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(identity.KeyActive); !errors.Is(err, identity.ErrNoKey) {
		t.Fatalf("Load after Delete = %v", err)
	}
}

// The blob holds only the TPM-wrapped private part; the key is created fixedTPM and fixedParent,
// and a blob of an exportable key is refused.
func TestTPMKeyIsNotExportable(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	defer func() { _ = s.Close() }()
	k, err := s.Generate(identity.KeyActive)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := any(k).(interface{ Bytes() ([]byte, error) }); ok {
		t.Fatal("the TPM key exposes its private scalar")
	}
	path := filepath.Join(dir, identity.KeyActive+".tpm")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var blob struct {
		Version int    `json:"version"`
		Public  []byte `json:"public"`
		Private []byte `json:"private"`
	}
	if err := json.Unmarshal(data, &blob); err != nil {
		t.Fatal(err)
	}
	pub, err := tpm2.Unmarshal[tpm2.TPM2BPublic](blob.Public)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := pub.Contents()
	if err != nil {
		t.Fatal(err)
	}
	a := contents.ObjectAttributes
	if !a.FixedTPM || !a.FixedParent || !a.SensitiveDataOrigin || a.Decrypt || a.Restricted {
		t.Fatalf("attributes %+v", a)
	}
	contents.ObjectAttributes.FixedTPM, contents.ObjectAttributes.FixedParent = false, false
	blob.Public = tpm2.Marshal(tpm2.New2B(*contents))
	tampered, _ := json.Marshal(blob)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(identity.KeyActive); err == nil {
		t.Fatal("loaded an exportable key")
	}
}
