package identity

import (
	"crypto"
	"crypto/ecdsa"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// DefaultTPMDevice is the kernel's resource-managed TPM 2.0 device.
const DefaultTPMDevice = "/dev/tpmrm0"

// TPMKeyStore makes identity keys inside a TPM 2.0 (KEY_PROTECTION_TPM): each key is created by
// the TPM under the owner hierarchy's standard ECC storage root key with fixedTPM, fixedParent and
// sensitiveDataOrigin set, so its private part never leaves the TPM unencrypted and cannot be
// duplicated. The store keeps the TPM-wrapped key blob as <dir>/<name>.tpm; deleting the blob
// erases the key.
type TPMKeyStore struct {
	blobStore

	mu      sync.Mutex
	tpm     transport.TPMCloser
	srk     *tpm2.CreatePrimaryResponse
	handles map[*tpmKey]struct{}
}

type tpmFile struct {
	Version int    `json:"version"`
	Public  []byte `json:"public"`
	Private []byte `json:"private"`
}

// OpenTPMKeyStore opens the TPM device (DefaultTPMDevice when empty) and keeps blobs in dir.
func OpenTPMKeyStore(dir, device string) (*TPMKeyStore, error) {
	if device == "" {
		device = DefaultTPMDevice
	}
	t, err := openTPM(device)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM %s: %w", device, err)
	}
	s, err := NewTPMKeyStore(dir, t)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return s, nil
}

// NewTPMKeyStore uses an open TPM transport, which the store closes.
func NewTPMKeyStore(dir string, t transport.TPMCloser) (*TPMKeyStore, error) {
	b, err := newBlobStore(dir, ".tpm")
	if err != nil {
		return nil, err
	}
	srk, err := createSRK(t)
	if err != nil {
		return nil, err
	}
	return &TPMKeyStore{blobStore: b, tpm: t, srk: srk, handles: map[*tpmKey]struct{}{}}, nil
}

// createSRK makes the owner hierarchy's standard ECC storage root key; the TPM derives the same
// key from its seed every time.
func createSRK(t transport.TPM) (*tpm2.CreatePrimaryResponse, error) {
	srk, err := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.TPMRHOwner,
		InPublic:      tpm2.New2B(tpm2.ECCSRKTemplate),
	}.Execute(t)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM storage root key: %w", err)
	}
	return srk, nil
}

func (s *TPMKeyStore) Protection() edgelinkv3.KeyProtection {
	return edgelinkv3.KeyProtection_KEY_PROTECTION_TPM
}

var tpmSigningTemplate = tpm2.TPMTPublic{
	Type:    tpm2.TPMAlgECC,
	NameAlg: tpm2.TPMAlgSHA256,
	ObjectAttributes: tpm2.TPMAObject{
		FixedTPM:            true,
		FixedParent:         true,
		SensitiveDataOrigin: true,
		UserWithAuth:        true,
		NoDA:                true,
		SignEncrypt:         true,
	},
	Parameters: tpm2.NewTPMUPublicParms(tpm2.TPMAlgECC, &tpm2.TPMSECCParms{
		Scheme: tpm2.TPMTECCScheme{
			Scheme:  tpm2.TPMAlgECDSA,
			Details: tpm2.NewTPMUAsymScheme(tpm2.TPMAlgECDSA, &tpm2.TPMSSigSchemeECDSA{HashAlg: tpm2.TPMAlgSHA256}),
		},
		CurveID: tpm2.TPMECCNistP256,
	}),
}

func (s *TPMKeyStore) Generate(name string) (Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tpm == nil {
		return nil, errors.New("identity: TPM key store closed")
	}
	rsp, err := tpm2.Create{
		ParentHandle: s.parent(),
		InPublic:     tpm2.New2B(tpmSigningTemplate),
	}.Execute(s.tpm)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM create key: %w", err)
	}
	data, err := json.Marshal(tpmFile{Version: 1, Public: tpm2.Marshal(rsp.OutPublic), Private: tpm2.Marshal(rsp.OutPrivate)})
	if err != nil {
		return nil, err
	}
	if err := s.create(name, data); err != nil {
		return nil, err
	}
	return s.load(rsp.OutPublic, rsp.OutPrivate)
}

func (s *TPMKeyStore) Load(name string) (Key, error) {
	data, err := s.read(name)
	if err != nil {
		return nil, err
	}
	var f tpmFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
		return nil, fmt.Errorf("identity: %s: not a TPM key file", s.path(name))
	}
	pub, err := tpm2.Unmarshal[tpm2.TPM2BPublic](f.Public)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
	}
	priv, err := tpm2.Unmarshal[tpm2.TPM2BPrivate](f.Private)
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w", s.path(name), err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tpm == nil {
		return nil, errors.New("identity: TPM key store closed")
	}
	return s.load(*pub, *priv)
}

func (s *TPMKeyStore) parent() tpm2.NamedHandle {
	return tpm2.NamedHandle{Handle: s.srk.ObjectHandle, Name: s.srk.Name}
}

func (s *TPMKeyStore) load(public tpm2.TPM2BPublic, private tpm2.TPM2BPrivate) (*tpmKey, error) {
	contents, err := public.Contents()
	if err != nil {
		return nil, err
	}
	if err := checkTPMTemplate(contents); err != nil {
		return nil, err
	}
	params, err := contents.Parameters.ECCDetail()
	if err != nil {
		return nil, err
	}
	point, err := contents.Unique.ECC()
	if err != nil {
		return nil, err
	}
	pub, err := tpm2.ECDSAPub(params, point)
	if err != nil {
		return nil, err
	}
	rsp, err := tpm2.Load{ParentHandle: s.parent(), InPrivate: private, InPublic: public}.Execute(s.tpm)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM load key: %w", err)
	}
	k := &tpmKey{store: s, handle: tpm2.NamedHandle{Handle: rsp.ObjectHandle, Name: rsp.Name}, pub: pub}
	s.handles[k] = struct{}{}
	return k, nil
}

// checkTPMTemplate refuses a blob that is not a non-exportable P-256 signing key.
func checkTPMTemplate(p *tpm2.TPMTPublic) error {
	a := p.ObjectAttributes
	if p.Type != tpm2.TPMAlgECC || !a.FixedTPM || !a.FixedParent || !a.SensitiveDataOrigin || !a.SignEncrypt {
		return errors.New("identity: TPM key is not a non-exportable signing key")
	}
	ecc, err := p.Parameters.ECCDetail()
	if err != nil || ecc.CurveID != tpm2.TPMECCNistP256 {
		return errors.New("identity: TPM key is not on P-256")
	}
	return nil
}

func (s *TPMKeyStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tpm == nil {
		return nil
	}
	for k := range s.handles {
		_, _ = tpm2.FlushContext{FlushHandle: k.handle.Handle}.Execute(s.tpm)
	}
	s.handles = nil
	_, _ = tpm2.FlushContext{FlushHandle: s.srk.ObjectHandle}.Execute(s.tpm)
	err := s.tpm.Close()
	s.tpm = nil
	return err
}

type tpmKey struct {
	store  *TPMKeyStore
	handle tpm2.NamedHandle
	pub    *ecdsa.PublicKey
}

func (k *tpmKey) Public() crypto.PublicKey { return k.pub }

func (k *tpmKey) Protection() edgelinkv3.KeyProtection {
	return edgelinkv3.KeyProtection_KEY_PROTECTION_TPM
}

// Sign signs a SHA-256 digest in the TPM and returns an ASN.1 DER signature.
func (k *tpmKey) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.SHA256 || len(digest) != 32 {
		return nil, errors.New("identity: TPM keys sign SHA-256 digests only")
	}
	k.store.mu.Lock()
	defer k.store.mu.Unlock()
	if k.store.tpm == nil {
		return nil, errors.New("identity: TPM key store closed")
	}
	rsp, err := tpm2.Sign{
		KeyHandle: k.handle,
		Digest:    tpm2.TPM2BDigest{Buffer: digest},
		InScheme: tpm2.TPMTSigScheme{
			Scheme:  tpm2.TPMAlgECDSA,
			Details: tpm2.NewTPMUSigScheme(tpm2.TPMAlgECDSA, &tpm2.TPMSSchemeHash{HashAlg: tpm2.TPMAlgSHA256}),
		},
		Validation: tpm2.TPMTTKHashCheck{Tag: tpm2.TPMSTHashCheck, Hierarchy: tpm2.TPMRHNull},
	}.Execute(k.store.tpm)
	if err != nil {
		return nil, fmt.Errorf("identity: TPM sign: %w", err)
	}
	sig, err := rsp.Signature.Signature.ECDSA()
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(struct{ R, S *big.Int }{
		new(big.Int).SetBytes(sig.SignatureR.Buffer),
		new(big.Int).SetBytes(sig.SignatureS.Buffer),
	})
}
