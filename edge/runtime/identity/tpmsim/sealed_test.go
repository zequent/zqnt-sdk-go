package tpmsim

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-tpm-tools/simulator"
	"github.com/google/go-tpm/tpm2/transport"
	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
)

func openSealed(t *testing.T, dir string, seed int64) *identity.TPMSealedKeyStore {
	t.Helper()
	sim, err := simulator.GetWithFixedSeedInsecure(seed)
	if err != nil {
		t.Fatal(err)
	}
	s, err := identity.NewTPMSealedKeyStore(dir, transport.FromReadWriteCloser(sim))
	if err != nil {
		_ = sim.Close()
		t.Fatal(err)
	}
	return s
}

func TestTPMSealedKeyStore(t *testing.T) {
	dir := t.TempDir()
	s := openSealed(t, dir, 7)
	if s.Protection() != edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_SEALED {
		t.Fatalf("protection %v", s.Protection())
	}
	k, err := s.Generate(identity.KeyActive)
	if err != nil {
		t.Fatal(err)
	}
	if k.Protection() != edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_SEALED {
		t.Fatalf("key protection %v", k.Protection())
	}
	pub, err := identity.PublicKeyBytes(k)
	if err != nil {
		t.Fatal(err)
	}
	scalar, err := k.(interface{ Bytes() ([]byte, error) }).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(dir, identity.KeyActive+".tpmsealed"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, scalar) || strings.Contains(string(blob), hex.EncodeToString(scalar)) {
		t.Fatal("the sealed blob holds the private scalar in plain form")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openSealed(t, dir, 7)
	loaded, err := s.Load(identity.KeyActive)
	if err != nil {
		t.Fatal(err)
	}
	transcript := []byte("zqnt-edge-link-v1 hello test")
	sig, err := handshake.Sign(loaded, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake.Verify(handshake.AlgECDSAP256, pub, transcript, sig); err != nil {
		t.Fatalf("signature of the unsealed key: %v", err)
	}
	state := &identity.State{IdentityKeyID: handshake.KeyID(handshake.AlgECDSAP256, pub), Suite: 0x0101}
	p, err := identity.NewProvider(identity.Dir(dir), s, state)
	if err != nil {
		t.Fatal(err)
	}
	if p.Protection() != "KEY_PROTECTION_SOFTWARE_SEALED" {
		t.Fatalf("provider protection %s", p.Protection())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	other := openSealed(t, dir, 8)
	defer func() { _ = other.Close() }()
	if _, err := other.Load(identity.KeyActive); err == nil {
		t.Fatal("another TPM unsealed the key")
	}
	if err := other.Delete(identity.KeyActive); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Load(identity.KeyActive); !errors.Is(err, identity.ErrNoKey) {
		t.Fatalf("Load after Delete = %v", err)
	}
}

func TestTPMSealedKeyRefusesAnUnboundObject(t *testing.T) {
	dir := t.TempDir()
	s := openSealed(t, dir, 7)
	defer func() { _ = s.Close() }()
	if _, err := s.Generate(identity.KeyActive); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, identity.KeyActive+".tpmsealed")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	f["public_key"] = []byte("not the sealed key")
	tampered, _ := json.Marshal(f)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(identity.KeyActive); err == nil {
		t.Fatal("loaded a sealed key whose public key was changed")
	}
}
