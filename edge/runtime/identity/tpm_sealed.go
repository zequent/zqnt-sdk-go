package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// TPMSealedKeyStore keeps software P-256 keys sealed by a TPM 2.0 (KEY_PROTECTION_SOFTWARE_SEALED):
// the private scalar is a sealed data object under the TPM's storage root key, fixedTPM and
// fixedParent, so only this TPM can unseal it. Scalars travel to and from the TPM in
// parameter-encrypted sessions salted to the storage root key, and are held in memory only
// while the runtime runs. For boards whose TPM cannot keep the key itself; the seal is not bound
// to PCRs. The blob is <dir>/<name>.tpmsealed.
type TPMSealedKeyStore struct {
	blobStore

	mu  sync.Mutex
	tpm transport.TPMCloser
	srk *tpm2.CreatePrimaryResponse
}

type tpmSealedFile struct {
	Version   int    `json:"version"`
	PublicKey []byte `json:"public_key"`
	Public    []byte `json:"public"`
	Private   []byte `json:"private"`
}

// OpenTPMSealedKeyStore opens the TPM device (DefaultTPMDevice when empty) and keeps blobs in dir.
func OpenTPMSealedKeyStore(dir, device string) (*TPMSealedKeyStore, error) {
	if device == "" {
		device = DefaultTPMDevice
	}
	t, err := openTPM(device)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM %s: %w", device, err)
	}
	s, err := NewTPMSealedKeyStore(dir, t)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return s, nil
}

// NewTPMSealedKeyStore uses an open TPM transport, which the store closes.
func NewTPMSealedKeyStore(dir string, t transport.TPMCloser) (*TPMSealedKeyStore, error) {
	b, err := newBlobStore(dir, ".tpmsealed")
	if err != nil {
		return nil, err
	}
	srk, err := createSRK(t)
	if err != nil {
		return nil, err
	}
	return &TPMSealedKeyStore{blobStore: b, tpm: t, srk: srk}, nil
}

func (s *TPMSealedKeyStore) Protection() edgelinkv3.KeyProtection {
	return edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_SEALED
}

var tpmSealTemplate = tpm2.TPMTPublic{
	Type:    tpm2.TPMAlgKeyedHash,
	NameAlg: tpm2.TPMAlgSHA256,
	ObjectAttributes: tpm2.TPMAObject{
		FixedTPM:     true,
		FixedParent:  true,
		UserWithAuth: true,
		NoDA:         true,
	},
}

// session is an HMAC session salted to the storage root key that encrypts the parameters in dir.
func (s *TPMSealedKeyStore) session(dir tpm2.AuthOption) (tpm2.Session, error) {
	pub, err := s.srk.OutPublic.Contents()
	if err != nil {
		return nil, err
	}
	return tpm2.HMAC(tpm2.TPMAlgSHA256, 16, dir, tpm2.Salted(s.srk.ObjectHandle, *pub)), nil
}

func (s *TPMSealedKeyStore) parent(dir tpm2.AuthOption) (tpm2.AuthHandle, error) {
	sess, err := s.session(dir)
	return tpm2.AuthHandle{Handle: s.srk.ObjectHandle, Name: s.srk.Name, Auth: sess}, err
}

func (s *TPMSealedKeyStore) Generate(name string) (Key, error) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tpm == nil {
		return nil, errors.New("identity: TPM key store closed")
	}
	parent, err := s.parent(tpm2.AESEncryption(128, tpm2.EncryptIn))
	if err != nil {
		return nil, err
	}
	rsp, err := tpm2.Create{
		ParentHandle: parent,
		InSensitive: tpm2.TPM2BSensitiveCreate{Sensitive: &tpm2.TPMSSensitiveCreate{
			Data: tpm2.NewTPMUSensitiveCreate(&tpm2.TPM2BSensitiveData{Buffer: scalar}),
		}},
		InPublic: tpm2.New2B(tpmSealTemplate),
	}.Execute(s.tpm)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM seal key: %w", err)
	}
	data, err := json.Marshal(tpmSealedFile{
		Version: 1, PublicKey: pub, Public: tpm2.Marshal(rsp.OutPublic), Private: tpm2.Marshal(rsp.OutPrivate),
	})
	if err != nil {
		return nil, err
	}
	if err := s.create(name, data); err != nil {
		return nil, err
	}
	return softwareKey{priv, s.Protection()}, nil
}

func (s *TPMSealedKeyStore) Load(name string) (Key, error) {
	data, err := s.read(name)
	if err != nil {
		return nil, err
	}
	var f tpmSealedFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
		return nil, fmt.Errorf("identity: %s: not a TPM-sealed key file", s.path(name))
	}
	public, err := tpm2.Unmarshal[tpm2.TPM2BPublic](f.Public)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
	}
	private, err := tpm2.Unmarshal[tpm2.TPM2BPrivate](f.Private)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
	}
	contents, err := public.Contents()
	if err != nil || contents.Type != tpm2.TPMAlgKeyedHash || !contents.ObjectAttributes.FixedTPM || !contents.ObjectAttributes.FixedParent {
		return nil, fmt.Errorf("identity: %s: not a TPM-bound sealed object", s.path(name))
	}
	scalar, err := s.unseal(*public, *private)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
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

func (s *TPMSealedKeyStore) unseal(public tpm2.TPM2BPublic, private tpm2.TPM2BPrivate) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tpm == nil {
		return nil, errors.New("identity: TPM key store closed")
	}
	loaded, err := tpm2.Load{
		ParentHandle: tpm2.NamedHandle{Handle: s.srk.ObjectHandle, Name: s.srk.Name},
		InPrivate:    private,
		InPublic:     public,
	}.Execute(s.tpm)
	if err != nil {
		return nil, fmt.Errorf("TPM load sealed key (sealed by another TPM?): %w", err)
	}
	defer func() { _, _ = tpm2.FlushContext{FlushHandle: loaded.ObjectHandle}.Execute(s.tpm) }()
	sess, err := s.session(tpm2.AESEncryption(128, tpm2.EncryptOut))
	if err != nil {
		return nil, err
	}
	rsp, err := tpm2.Unseal{
		ItemHandle: tpm2.AuthHandle{Handle: loaded.ObjectHandle, Name: loaded.Name, Auth: sess},
	}.Execute(s.tpm)
	if err != nil {
		return nil, fmt.Errorf("TPM unseal: %w", err)
	}
	return rsp.OutData.Buffer, nil
}

func (s *TPMSealedKeyStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tpm == nil {
		return nil
	}
	_, _ = tpm2.FlushContext{FlushHandle: s.srk.ObjectHandle}.Execute(s.tpm)
	err := s.tpm.Close()
	s.tpm = nil
	return err
}
