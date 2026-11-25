// Package identitytest is an in-process platform for tests of the node identity: an installation
// anchor and handshake key that sign Welcome and HelloRetry frames, an EdgeEnrollmentService, TLS
// credentials for localhost, and enrolled development identities. It uses the zqnt-api handshake
// helper, as the platform does.
package identitytest

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
)

// Key is a P-256 key with its encoded public key and key id.
type Key struct {
	Private *ecdsa.PrivateKey
	Public  []byte
	ID      []byte
}

// NewKey generates a P-256 key.
func NewKey(t testing.TB) Key {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return Key{Private: priv, Public: pub, ID: handshake.KeyID(handshake.AlgECDSAP256, pub)}
}

// Sign signs a transcript, r || s.
func (k Key) Sign(t testing.TB, transcript []byte) []byte {
	t.Helper()
	sig, err := handshake.Sign(k.Private, transcript)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// Authority is one installation's anchor and an edge-link replica's handshake key.
type Authority struct {
	t              testing.TB
	InstallationID string
	Anchor         Key
	Handshake      Key
	NotBefore      time.Time
	NotAfter       time.Time
}

// NewAuthority makes an installation whose handshake key is valid from 5 minutes ago for 48 hours.
func NewAuthority(t testing.TB) *Authority {
	now := time.Now()
	return &Authority{
		t:              t,
		InstallationID: "inst-" + uuid.NewString()[:8],
		Anchor:         NewKey(t),
		Handshake:      NewKey(t),
		NotBefore:      now.Add(-5 * time.Minute),
		NotAfter:       now.Add(48 * time.Hour),
	}
}

// Anchors are the anchors a node pins.
func (a *Authority) Anchors() []identity.Anchor {
	return []identity.Anchor{{KeyID: a.Anchor.ID, Alg: uint32(handshake.AlgECDSAP256), PublicKey: a.Anchor.Public}}
}

// TrustAnchors are the anchors as CompleteEnrollment returns them.
func (a *Authority) TrustAnchors() []*edgelinkv3.TrustAnchor {
	return []*edgelinkv3.TrustAnchor{{KeyId: a.Anchor.ID, Alg: uint32(handshake.AlgECDSAP256), PublicKey: a.Anchor.Public}}
}

// PlatformKey is the anchor-signed certificate of the handshake key.
func (a *Authority) PlatformKey() *edgelinkv3.PlatformKey {
	k := &edgelinkv3.PlatformKey{
		InstallationId: a.InstallationID,
		AnchorKeyId:    a.Anchor.ID,
		KeyId:          a.Handshake.ID,
		KeyAlg:         uint32(handshake.AlgECDSAP256),
		PublicKey:      a.Handshake.Public,
		NotBefore:      a.NotBefore.Unix(),
		NotAfter:       a.NotAfter.Unix(),
	}
	transcript, err := handshake.PlatformKeyTranscript(k)
	if err != nil {
		a.t.Fatal(err)
	}
	k.Signature = a.Anchor.Sign(a.t, transcript)
	return k
}

// VerifyHello checks a Hello the way edge-link does (signature, key id, suite, sizes) and
// returns its transcript.
func VerifyHello(h *edgelinkv3.Hello, organizationID string, binding, nodePublicKey []byte) ([]byte, error) {
	if !identity.SupportedSuite(h.GetCryptoSuite()) || len(h.GetEphemeralPub()) != handshake.EphemeralKeySize ||
		string(h.GetIdentityKeyId()) != string(handshake.KeyID(handshake.AlgECDSAP256, nodePublicKey)) {
		return nil, handshake.ErrSignature
	}
	transcript := handshake.HelloTranscript(h, organizationID, binding)
	return transcript, handshake.Verify(handshake.AlgECDSAP256, nodePublicKey, transcript, h.GetSignature())
}

// Welcome completes w as the answer to the Hello with transcript helloTranscript: platform key,
// a fresh ephemeral key and the signature. It returns the platform's session keys.
func (a *Authority) Welcome(helloTranscript []byte, hello *edgelinkv3.Hello, w *edgelinkv3.Welcome) *handshake.SessionKeys {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		a.t.Fatal(err)
	}
	w.PlatformKey = a.PlatformKey()
	w.EphemeralPub = eph.PublicKey().Bytes()
	transcript := handshake.WelcomeTranscript(handshake.Hash(helloTranscript), w)
	w.Signature = a.Handshake.Sign(a.t, transcript)
	shared, err := handshake.SharedSecret(eph, hello.GetEphemeralPub())
	if err != nil {
		a.t.Fatal(err)
	}
	keys, err := handshake.DeriveSessionKeys(hello.GetCryptoSuite(), shared, helloTranscript, transcript)
	if err != nil {
		a.t.Fatal(err)
	}
	return keys
}

// HelloRetry answers the Hello with transcript helloTranscript with a fresh nonce and the
// platform's time.
func (a *Authority) HelloRetry(helloTranscript []byte, platformTime time.Time) *edgelinkv3.HelloRetry {
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	r := &edgelinkv3.HelloRetry{
		ServerNonce:  nonce,
		PlatformTime: timestamppb.New(platformTime),
		Reason:       edgelinkv3.HelloRetryReason_HELLO_RETRY_REASON_CLOCK_UNTRUSTED,
		PlatformKey:  a.PlatformKey(),
	}
	transcript, err := handshake.HelloRetryTranscript(handshake.Hash(helloTranscript), r)
	if err != nil {
		a.t.Fatal(err)
	}
	r.Signature = a.Handshake.Sign(a.t, transcript)
	return r
}

// Enrolled is a node enrolled with a development key in a temporary identity directory.
type Enrolled struct {
	Dir   identity.Dir
	Store *identity.DevKeyStore
	State *identity.State
	// Key is the node's public identity key (the private key stays in Store).
	Key Key
}

// Enroll writes an enrollment for a new node of a's installation: a development identity key,
// node.json and a credential valid for a year.
func Enroll(t testing.TB, a *Authority, organizationID string) *Enrolled {
	t.Helper()
	dir := identity.Dir(filepath.Join(t.TempDir(), "identity"))
	store, err := identity.NewDevKeyStore(string(dir))
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.Generate(identity.KeyActive)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := identity.PublicKeyBytes(key)
	if err != nil {
		t.Fatal(err)
	}
	e := &Enrolled{Dir: dir, Store: store, Key: Key{Public: pub, ID: handshake.KeyID(handshake.AlgECDSAP256, pub)}}
	e.State = &identity.State{
		Version:        1,
		NodeID:         uuid.NewString(),
		OrganizationID: organizationID,
		InstallationID: a.InstallationID,
		NodeClass:      identity.ClassInfrastructure,
		Anchors:        a.Anchors(),
		AllowedSuites:  []uint32{uint32(edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_CHACHA20_POLY1305)},
		Suite:          uint32(edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_CHACHA20_POLY1305),
		IdentityKeyID:  e.Key.ID,
		KeyProtection:  key.Protection().String(),
		Platform:       identity.Platform{Endpoint: "127.0.0.1:1", Insecure: true},
		EnrolledAt:     time.Now().UTC(),
	}
	if err := dir.SaveState(e.State); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := dir.SaveCredential("test-credential", identity.CredentialMeta{IssuedAt: now, ExpiresAt: now.Add(365 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return e
}

// TLS is a self-signed certificate for localhost and 127.0.0.1.
type TLS struct {
	// Server is the transport credentials of a gRPC server (TLS 1.3 only).
	Server credentials.TransportCredentials
	// CAFile is the certificate in PEM, for the node's ca_file.
	CAFile string
}

// NewTLS makes the certificate and writes its PEM to a temporary file.
func NewTLS(t testing.TB) TLS {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "edge-link test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	return TLS{
		Server: credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}),
		CAFile: caFile,
	}
}
