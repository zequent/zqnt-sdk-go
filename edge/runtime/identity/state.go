package identity

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// Files in the identity directory.
const (
	StateFile          = "node.json"
	CredentialFile     = "credential"
	CredentialMetaFile = "credential.json"
	HighWaterFile      = "time.hwm"
	CAFile             = "ca.pem"
)

// ErrNotEnrolled is returned when the identity directory holds no enrollment.
var ErrNotEnrolled = errors.New("identity: node is not enrolled (run zqnt-edge-node enroll)")

// HexBytes is written as lowercase hex in the state file.
type HexBytes []byte

func (h HexBytes) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(h)), nil }

func (h *HexBytes) UnmarshalText(text []byte) error {
	b, err := hex.DecodeString(string(text))
	*h = b
	return err
}

// Anchor is a pinned platform anchor key.
type Anchor struct {
	KeyID     HexBytes `json:"key_id"`
	Alg       uint32   `json:"alg"`
	PublicKey []byte   `json:"public_key,omitempty"`
}

// Platform is where the node enrolled and renews its credential.
type Platform struct {
	Endpoint   string `json:"endpoint"`
	ServerName string `json:"server_name,omitempty"`
	// CAFile holds PEM roots for the platform certificate; empty uses the system roots.
	CAFile   string `json:"ca_file,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}

// State is what enrollment returned, kept in node.json. Transcripts use exactly these values.
type State struct {
	Version          int       `json:"version"`
	NodeID           string    `json:"node_id"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationName string    `json:"organization_name,omitempty"`
	InstallationID   string    `json:"installation_id"`
	NodeClass        string    `json:"node_class"`
	Anchors          []Anchor  `json:"anchors"`
	AllowedSuites    []uint32  `json:"allowed_suites,omitempty"`
	Suite            uint32    `json:"suite"`
	IdentityKeyID    HexBytes  `json:"identity_key_id"`
	KeyProtection    string    `json:"key_protection"`
	Platform         Platform  `json:"platform"`
	EnrolledAt       time.Time `json:"enrolled_at"`
}

// Validate checks that the state can be used in transcripts.
func (s *State) Validate() error {
	var errs []error
	for name, id := range map[string]string{"node_id": s.NodeID, "organization_id": s.OrganizationID} {
		if !isCanonicalUUID(id) {
			errs = append(errs, fmt.Errorf("%s %q is not a lowercase UUID", name, id))
		}
	}
	if s.InstallationID == "" {
		errs = append(errs, errors.New("installation_id is empty"))
	}
	if len(s.Anchors) == 0 {
		errs = append(errs, errors.New("no platform anchor is pinned"))
	}
	for i, a := range s.Anchors {
		if a.Alg != uint32(handshake.AlgECDSAP256) || !bytes.Equal(a.KeyID, handshake.KeyID(uint8(a.Alg), a.PublicKey)) {
			errs = append(errs, fmt.Errorf("anchors[%d]: not a P-256 key matching its key id", i))
		}
	}
	if !SupportedSuite(s.Suite) {
		errs = append(errs, fmt.Errorf("suite %#04x is not supported", s.Suite))
	}
	if len(s.IdentityKeyID) != handshake.KeyIDSize {
		errs = append(errs, errors.New("identity_key_id is not 16 bytes"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("identity: %s: %w", StateFile, errors.Join(errs...))
	}
	return nil
}

func isCanonicalUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s
}

// SupportedSuite reports whether this runtime implements a crypto suite.
func SupportedSuite(suite uint32) bool {
	switch edgelinkv3.CryptoSuite(suite) {
	case edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_CHACHA20_POLY1305, edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_AES_256_GCM:
		return true
	}
	return false
}

// Dir is the identity directory: node.json, the credential, the time high-water mark and the
// key files of the file-based key stores.
type Dir string

func (d Dir) path(name string) string { return filepath.Join(string(d), name) }

// CredentialPath is the bearer credential file the IP session sends.
func (d Dir) CredentialPath() string { return d.path(CredentialFile) }

// LoadState reads node.json; ErrNotEnrolled when it does not exist.
func (d Dir) LoadState() (*State, error) {
	data, err := os.ReadFile(d.path(StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("identity: %s: %w", d.path(StateFile), err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveState writes node.json atomically.
func (d Dir) SaveState(s *State) error {
	if err := s.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(d.path(StateFile), append(data, '\n'), 0o600)
}

// CredentialMeta is when the stored credential was issued and expires.
type CredentialMeta struct {
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RenewAt is when a third of the credential's lifetime remains.
func (m CredentialMeta) RenewAt() time.Time {
	return m.ExpiresAt.Add(-m.ExpiresAt.Sub(m.IssuedAt) / 3)
}

// SaveCredential writes the credential and its lifetime, each atomically.
func (d Dir) SaveCredential(token string, meta CredentialMeta) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("identity: empty credential")
	}
	if err := writeFileAtomic(d.CredentialPath(), []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("identity: credential: %w", err)
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return writeFileAtomic(d.path(CredentialMetaFile), data, 0o600)
}

// Credential reads the credential and its lifetime.
func (d Dir) Credential() (string, CredentialMeta, error) {
	var meta CredentialMeta
	token, err := os.ReadFile(d.CredentialPath())
	if err != nil {
		return "", meta, fmt.Errorf("identity: credential: %w", err)
	}
	data, err := os.ReadFile(d.path(CredentialMetaFile))
	if err != nil {
		return "", meta, fmt.Errorf("identity: credential: %w", err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", meta, fmt.Errorf("identity: %s: %w", d.path(CredentialMetaFile), err)
	}
	return strings.TrimSpace(string(token)), meta, nil
}

// Erase removes the enrollment: node.json, the credential and the time high-water mark. Keys are
// erased through their key store.
func (d Dir) Erase() error {
	var errs []error
	for _, name := range []string{CredentialFile, CredentialMetaFile, StateFile, HighWaterFile} {
		if err := os.Remove(d.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Trust is the node's trust store: the pinned anchors and installation from node.json and the
// persisted time high-water mark. It verifies the platform's handshake key certificates.
type Trust struct {
	state *State
	path  string

	mu  sync.Mutex
	hwm time.Time
}

// Trust opens the trust store of an enrolled node.
func (d Dir) Trust(s *State) (*Trust, error) {
	t := &Trust{state: s, path: d.path(HighWaterFile)}
	data, err := os.ReadFile(t.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("identity: %w", err)
	default:
		sec, perr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if perr != nil {
			return nil, fmt.Errorf("identity: %s: %w", t.path, perr)
		}
		t.hwm = time.Unix(sec, 0)
	}
	return t, nil
}

// OrganizationID is the organization the node enrolled in.
func (t *Trust) OrganizationID() string { return t.state.OrganizationID }

// HighWater is the latest authenticated platform time the node has seen.
func (t *Trust) HighWater() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hwm
}

// RaiseHighWater persists ts when it is later than the high-water mark.
func (t *Trust) RaiseHighWater(ts time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	ts = ts.Truncate(time.Second)
	if !ts.After(t.hwm) {
		return nil
	}
	if err := writeFileAtomic(t.path, []byte(strconv.FormatInt(ts.Unix(), 10)+"\n"), 0o600); err != nil {
		return fmt.Errorf("identity: time high-water mark: %w", err)
	}
	t.hwm = ts
	return nil
}

// Errors of VerifyPlatformKey.
var (
	ErrUnknownAnchor   = errors.New("identity: platform key is not signed by a pinned anchor")
	ErrPlatformKey     = errors.New("identity: platform key certificate invalid")
	ErrPlatformKeyTime = errors.New("identity: platform key certificate not valid now")
)

// VerifyPlatformKey checks a platform handshake key certificate: signed by a pinned anchor for
// this installation, a P-256 key matching its key id, and valid at now when the node's time is
// trusted, or valid beyond the time high-water mark when it is not.
func (t *Trust) VerifyPlatformKey(k *edgelinkv3.PlatformKey, now time.Time, trusted bool) error {
	if k == nil {
		return fmt.Errorf("%w: missing", ErrPlatformKey)
	}
	var anchor *Anchor
	for i := range t.state.Anchors {
		if bytes.Equal(t.state.Anchors[i].KeyID, k.GetAnchorKeyId()) {
			anchor = &t.state.Anchors[i]
		}
	}
	if anchor == nil {
		return ErrUnknownAnchor
	}
	if k.GetInstallationId() != t.state.InstallationID {
		return fmt.Errorf("%w: installation %q, pinned %q", ErrPlatformKey, k.GetInstallationId(), t.state.InstallationID)
	}
	if k.GetKeyAlg() != uint32(handshake.AlgECDSAP256) || !bytes.Equal(k.GetKeyId(), handshake.KeyID(handshake.AlgECDSAP256, k.GetPublicKey())) {
		return fmt.Errorf("%w: not a P-256 key matching its key id", ErrPlatformKey)
	}
	if _, err := handshake.ParsePublicKey(k.GetPublicKey()); err != nil {
		return fmt.Errorf("%w: %v", ErrPlatformKey, err)
	}
	transcript, err := handshake.PlatformKeyTranscript(k)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPlatformKey, err)
	}
	if err := handshake.Verify(uint8(anchor.Alg), anchor.PublicKey, transcript, k.GetSignature()); err != nil {
		return fmt.Errorf("%w: anchor signature: %v", ErrPlatformKey, err)
	}
	notBefore, notAfter := time.Unix(k.GetNotBefore(), 0), time.Unix(k.GetNotAfter(), 0)
	if trusted {
		if now.Before(notBefore) || now.After(notAfter) {
			return fmt.Errorf("%w: valid %s to %s, now %s", ErrPlatformKeyTime,
				notBefore.UTC().Format(time.RFC3339), notAfter.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
		}
		return nil
	}
	if !notAfter.After(t.HighWater()) {
		return fmt.Errorf("%w: expired %s, before the last platform time seen %s", ErrPlatformKeyTime,
			notAfter.UTC().Format(time.RFC3339), t.HighWater().UTC().Format(time.RFC3339))
	}
	return nil
}
