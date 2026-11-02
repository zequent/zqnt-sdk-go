package identity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

func sealingKey() []byte { return bytes.Repeat([]byte{0x42}, SealingKeySize) }

func stores(t *testing.T) map[string]KeyStore {
	dev, err := NewDevKeyStore(filepath.Join(t.TempDir(), "dev"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := NewSealedKeyStore(filepath.Join(t.TempDir(), "sealed"), sealingKey())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]KeyStore{"dev": dev, "sealed": sealed}
}

func TestKeyStoreLifecycle(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Load(KeyActive); !errors.Is(err, ErrNoKey) {
				t.Fatalf("Load of a missing key = %v", err)
			}
			k, err := s.Generate(KeyNext)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Generate(KeyNext); err == nil {
				t.Fatal("Generate overwrote a key")
			}
			if k.Protection() != s.Protection() {
				t.Fatalf("protection %v, store %v", k.Protection(), s.Protection())
			}
			pub, err := PublicKeyBytes(k)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Rename(KeyNext, KeyActive); err != nil {
				t.Fatal(err)
			}
			loaded, err := s.Load(KeyActive)
			if err != nil {
				t.Fatal(err)
			}
			transcript := []byte("zqnt test transcript")
			sig, err := handshake.Sign(loaded, transcript)
			if err != nil {
				t.Fatal(err)
			}
			if err := handshake.Verify(handshake.AlgECDSAP256, pub, transcript, sig); err != nil {
				t.Fatalf("signature of the reloaded key: %v", err)
			}
			if runtime.GOOS != "windows" {
				matches, _ := filepath.Glob(filepath.Join(filepath.Dir(blobPath(s, KeyActive)), "*"))
				for _, m := range matches {
					if fi, err := os.Stat(m); err != nil || fi.Mode().Perm() != 0o600 {
						t.Errorf("%s: mode %v", m, fi.Mode().Perm())
					}
				}
			}
			if err := s.Delete(KeyActive); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(KeyActive); err != nil {
				t.Fatalf("deleting a missing key: %v", err)
			}
			if _, err := s.Load(KeyActive); !errors.Is(err, ErrNoKey) {
				t.Fatalf("Load after Delete = %v", err)
			}
		})
	}
}

func blobPath(s KeyStore, name string) string {
	switch s := s.(type) {
	case *DevKeyStore:
		return s.path(name)
	case *SealedKeyStore:
		return s.path(name)
	}
	return ""
}

func TestSealedKeyIsEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSealedKeyStore(dir, sealingKey())
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.Generate(KeyActive)
	if err != nil {
		t.Fatal(err)
	}
	scalar, err := k.(softwareKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.path(KeyActive))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, scalar) || strings.Contains(string(data), hex.EncodeToString(scalar)) {
		t.Fatal("the sealed file holds the private scalar in plain form")
	}
	other, err := NewSealedKeyStore(dir, bytes.Repeat([]byte{1}, SealingKeySize))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Load(KeyActive); err == nil {
		t.Fatal("unsealed with another sealing key")
	}
	var f sealedFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	f.PublicKey[10] ^= 1
	tampered, _ := json.Marshal(f)
	if err := os.WriteFile(s.path(KeyActive), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(KeyActive); err == nil {
		t.Fatal("loaded a sealed file whose public key was changed")
	}
}

func TestSealingKeyFormats(t *testing.T) {
	raw := sealingKey()
	for name, text := range map[string]string{
		"hex":       hex.EncodeToString(raw) + "\n",
		"base64":    "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI=",
		"base64url": "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI",
		"raw":       string(raw),
	} {
		got, err := ParseSealingKey([]byte(text))
		if err != nil || !bytes.Equal(got, raw) {
			t.Errorf("%s: %x, %v", name, got, err)
		}
	}
	if _, err := ParseSealingKey([]byte("short")); err == nil {
		t.Error("accepted a short sealing key")
	}
	if runtime.GOOS == "windows" {
		return
	}
	path := filepath.Join(t.TempDir(), "sealing.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(raw)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSealingKeyFile(path); err == nil {
		t.Error("read a world-readable sealing key")
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadSealingKeyFile(path); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("ReadSealingKeyFile = %x, %v", got, err)
	}
}

// The vectors' node identity key, in a development and a sealed store, has the vectors' key id and
// signs transcripts that verify with the vectors' public key.
func TestVectorIdentityKeyInEveryStore(t *testing.T) {
	v := loadVectors(t)
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), v.nodePrivate)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := NewDevKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.create(KeyActive, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		t.Fatal(err)
	}
	sealed, err := NewSealedKeyStore(t.TempDir(), sealingKey())
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, sealed.aead.NonceSize())
	f := sealedFile{Version: 1, PublicKey: v.nodePublic, Nonce: nonce, Ciphertext: sealed.aead.Seal(nil, nonce, v.nodePrivate, sealedAAD(v.nodePublic))}
	data, _ := json.Marshal(f)
	if err := sealed.create(KeyActive, data); err != nil {
		t.Fatal(err)
	}

	for name, s := range map[string]KeyStore{"dev": dev, "sealed": sealed} {
		state := &State{IdentityKeyID: v.nodeKeyID, Suite: 0x0101}
		p, err := NewProvider(Dir(t.TempDir()), s, state)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(p.KeyID(), v.nodeKeyID) {
			t.Fatalf("%s: key id %x, vector %x", name, p.KeyID(), v.nodeKeyID)
		}
		for what, transcript := range map[string][]byte{"hello": v.hello, "proof": v.proof, "renew": v.renew} {
			sig, err := p.Sign(p.KeyID(), transcript)
			if err != nil {
				t.Fatal(err)
			}
			if err := handshake.Verify(handshake.AlgECDSAP256, v.nodePublic, transcript, sig); err != nil {
				t.Errorf("%s: %s signature does not verify: %v", name, what, err)
			}
		}
	}
}

type keyVectors struct {
	nodePrivate, nodePublic, nodeKeyID []byte
	hello, proof, renew                []byte
}

func loadVectors(t *testing.T) keyVectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "edge-link-handshake-v1.vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Inputs struct {
			NodeIdentityPrivate string `json:"node_identity_private"`
		} `json:"inputs"`
		Keys struct {
			NodeIdentityPublic string `json:"node_identity_public"`
			NodeIdentityKeyID  string `json:"node_identity_key_id"`
		} `json:"keys"`
		Hello             struct{ Transcript string } `json:"hello"`
		EnrollmentProof   struct{ Transcript string } `json:"enrollment_proof"`
		CredentialRenewal struct{ Transcript string } `json:"credential_renewal"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	dec := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return keyVectors{
		nodePrivate: dec(raw.Inputs.NodeIdentityPrivate),
		nodePublic:  dec(raw.Keys.NodeIdentityPublic),
		nodeKeyID:   dec(raw.Keys.NodeIdentityKeyID),
		hello:       dec(raw.Hello.Transcript),
		proof:       dec(raw.EnrollmentProof.Transcript),
		renew:       dec(raw.CredentialRenewal.Transcript),
	}
}

func TestPublicKeyBytesRefusesOtherCurves(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PublicKeyBytes(softwareKey{k, edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_DEV}); err == nil {
		t.Fatal("accepted a P-384 key")
	}
}
