package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// softwareKey is a P-256 key held in memory.
type softwareKey struct {
	*ecdsa.PrivateKey
	protection edgelinkv3.KeyProtection
}

func (k softwareKey) Protection() edgelinkv3.KeyProtection { return k.protection }

// DevKeyStore keeps plain PKCS #8 key files (SOFTWARE_DEV): development, simulators and CI only.
type DevKeyStore struct{ blobStore }

// NewDevKeyStore keeps keys as <dir>/<name>.dev.pem.
func NewDevKeyStore(dir string) (*DevKeyStore, error) {
	b, err := newBlobStore(dir, ".dev.pem")
	if err != nil {
		return nil, err
	}
	return &DevKeyStore{b}, nil
}

func (s *DevKeyStore) Protection() edgelinkv3.KeyProtection {
	return edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_DEV
}

func (s *DevKeyStore) Generate(name string) (Key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	defer clear(der)
	if err := s.create(name, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return softwareKey{priv, s.Protection()}, nil
}

func (s *DevKeyStore) Load(name string) (Key, error) {
	data, err := s.read(name)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("identity: %s: no PRIVATE KEY block", s.path(name))
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, fmt.Errorf("identity: %s: not an ECDSA P-256 key", s.path(name))
	}
	return softwareKey{priv, s.Protection()}, nil
}

func (s *DevKeyStore) Close() error { return nil }

// SealingKeySize is the length of the key that seals software identity keys.
const SealingKeySize = 32

const sealedLabel = "zqnt-edge-identity-sealed-v1"

// SealedKeyStore keeps P-256 private scalars encrypted at rest with AES-256-GCM under a sealing
// key (SOFTWARE_SEALED); the scalar is decrypted into memory only while the runtime runs.
type SealedKeyStore struct {
	blobStore
	aead cipher.AEAD
}

type sealedFile struct {
	Version    int    `json:"version"`
	PublicKey  []byte `json:"public_key"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// NewSealedKeyStore keeps keys as <dir>/<name>.sealed, sealed under sealingKey (32 bytes).
func NewSealedKeyStore(dir string, sealingKey []byte) (*SealedKeyStore, error) {
	if len(sealingKey) != SealingKeySize {
		return nil, fmt.Errorf("identity: sealing key has %d bytes, want %d", len(sealingKey), SealingKeySize)
	}
	block, err := aes.NewCipher(sealingKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	b, err := newBlobStore(dir, ".sealed")
	if err != nil {
		return nil, err
	}
	return &SealedKeyStore{blobStore: b, aead: aead}, nil
}

func (s *SealedKeyStore) Protection() edgelinkv3.KeyProtection {
	return edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_SEALED
}

func (s *SealedKeyStore) Generate(name string) (Key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	scalar, err := priv.Bytes()
	if err != nil {
		return nil, err
	}
	defer clear(scalar)
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	f := sealedFile{Version: 1, PublicKey: pub, Nonce: nonce, Ciphertext: s.aead.Seal(nil, nonce, scalar, sealedAAD(pub))}
	data, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	if err := s.create(name, data); err != nil {
		return nil, err
	}
	return softwareKey{priv, s.Protection()}, nil
}

func (s *SealedKeyStore) Load(name string) (Key, error) {
	data, err := s.read(name)
	if err != nil {
		return nil, err
	}
	var f sealedFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
		return nil, fmt.Errorf("identity: %s: not a sealed key file", s.path(name))
	}
	if len(f.Nonce) != s.aead.NonceSize() {
		return nil, fmt.Errorf("identity: %s: bad nonce", s.path(name))
	}
	scalar, err := s.aead.Open(nil, f.Nonce, f.Ciphertext, sealedAAD(f.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("identity: %s: cannot unseal (wrong sealing key or damaged file)", s.path(name))
	}
	defer clear(scalar)
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), scalar)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil || string(pub) != string(f.PublicKey) {
		return nil, fmt.Errorf("identity: %s: public key does not match the sealed key", s.path(name))
	}
	return softwareKey{priv, s.Protection()}, nil
}

func (s *SealedKeyStore) Close() error { return nil }

func sealedAAD(pub []byte) []byte { return append([]byte(sealedLabel), pub...) }

// ReadSealingKeyFile reads a sealing key file (see ParseSealingKey). The file must not be
// accessible to others nor writable by its group.
func ReadSealingKeyFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("identity: sealing key: %w", err)
	}
	if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm&0o027 != 0 {
		return nil, fmt.Errorf("identity: sealing key %s has mode %#o: others must have no access and the group no write", path, perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("identity: sealing key: %w", err)
	}
	defer clear(data)
	return ParseSealingKey(data)
}

// ParseSealingKey reads a sealing key written as 64 hex digits, base64 of 32 bytes, or 32 raw bytes.
func ParseSealingKey(data []byte) ([]byte, error) {
	if len(data) == SealingKeySize {
		return append([]byte(nil), data...), nil
	}
	text := strings.TrimSpace(string(data))
	if b, err := hex.DecodeString(text); err == nil && len(b) == SealingKeySize {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(text); err == nil && len(b) == SealingKeySize {
			return b, nil
		}
	}
	return nil, errors.New("identity: sealing key must be 32 bytes, written as 64 hex digits or base64")
}
