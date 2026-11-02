// Package identity is the Edge Node's long-term identity (spec edge-link-handshake-v1): the ECDSA
// P-256 identity key in a key store (TPM 2.0, a sealed software key or a development key file),
// the enrollment state with the pinned platform anchors, the node-bound credential, and the
// enrollment, key rotation and credential renewal calls of EdgeEnrollmentService.
package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// Key names in a key store.
const (
	// KeyActive is the identity key the node signs with.
	KeyActive = "identity"
	// KeyNext is a key being rotated in: generated, not yet confirmed by the platform.
	KeyNext = "identity.next"
)

// ErrNoKey is returned by Load when the store holds no key of that name.
var ErrNoKey = errors.New("identity: no key")

// Key is an ECDSA P-256 identity key. Public returns *ecdsa.PublicKey; Sign takes a SHA-256
// digest and returns an ASN.1 DER signature.
type Key interface {
	crypto.Signer
	Protection() edgelinkv3.KeyProtection
}

// KeyStore holds named identity keys. Keys never leave it in plain form, except the development
// store's.
type KeyStore interface {
	// Protection is the key protection class of the keys this store makes.
	Protection() edgelinkv3.KeyProtection
	// Generate makes a new key under name; an existing key of that name is an error.
	Generate(name string) (Key, error)
	// Load opens the key under name, ErrNoKey when there is none.
	Load(name string) (Key, error)
	// Rename moves the key from one name to another, replacing what is there.
	Rename(from, to string) error
	// Delete erases the key under name; deleting a missing key is not an error.
	Delete(name string) error
	Close() error
}

// PublicKeyBytes is the key's public point, SEC 1 uncompressed (65 bytes).
func PublicKeyBytes(k crypto.Signer) ([]byte, error) {
	pub, ok := k.Public().(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("identity: key is not ECDSA P-256")
	}
	return pub.Bytes()
}

// blobStore keeps each key as one file <dir>/<name><ext>, written 0600 into a 0700 directory.
type blobStore struct {
	dir string
	ext string
}

func newBlobStore(dir, ext string) (blobStore, error) {
	if dir == "" {
		return blobStore{}, errors.New("identity: key directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return blobStore{}, fmt.Errorf("identity: %w", err)
	}
	return blobStore{dir: dir, ext: ext}, nil
}

func (b blobStore) path(name string) string { return filepath.Join(b.dir, name+b.ext) }

func (b blobStore) read(name string) ([]byte, error) {
	data, err := os.ReadFile(b.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoKey
	}
	return data, err
}

func (b blobStore) create(name string, data []byte) error {
	if _, err := os.Stat(b.path(name)); err == nil {
		return fmt.Errorf("identity: key %q exists", name)
	}
	return writeFileAtomic(b.path(name), data, 0o600)
}

func (b blobStore) Rename(from, to string) error {
	if err := os.Rename(b.path(from), b.path(to)); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	return syncDir(b.dir)
}

func (b blobStore) Delete(name string) error {
	path := b.path(name)
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		if fi, err := f.Stat(); err == nil {
			_, _ = f.Write(make([]byte, fi.Size()))
			_ = f.Sync()
		}
		_ = f.Close()
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("identity: %w", err)
	}
	return syncDir(b.dir)
}

// writeFileAtomic writes data to a temporary file next to path and renames it into place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	_ = d.Sync()
	return nil
}
