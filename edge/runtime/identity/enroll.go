package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"slices"
	"time"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tlsbinding"
)

// Dial connects to the platform's EdgeEnrollmentService over TLS 1.3 (or without TLS when
// Insecure, for local stacks and tests: the channel binding is then empty).
func (p Platform) Dial(extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	if p.Endpoint == "" {
		return nil, errors.New("identity: platform endpoint is required")
	}
	var opts []grpc.DialOption
	if p.Insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: p.ServerName}
		if p.CAFile != "" {
			pem, err := os.ReadFile(p.CAFile)
			if err != nil {
				return nil, fmt.Errorf("identity: ca_file: %w", err)
			}
			cfg.RootCAs = x509.NewCertPool()
			if !cfg.RootCAs.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("identity: ca_file %s: no certificates", p.CAFile)
			}
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	}
	conn, err := grpc.NewClient(p.Endpoint, append(opts, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	return conn, nil
}

// client runs one challenge-response exchange of EdgeEnrollmentService.
type client struct {
	api      edgelinkv3.EdgeEnrollmentServiceClient
	insecure bool
}

func (c client) binding(p *peer.Peer) ([]byte, error) {
	if c.insecure {
		return nil, nil
	}
	return tlsbinding.FromPeer(p)
}

// exchange gets a challenge for purpose and calls finish with it and the channel binding of the
// connection. The binding has to be that of the connection finish travels on: when the
// connection changed in between and finish failed, the exchange runs once more.
func (c client) exchange(ctx context.Context, purpose edgelinkv3.EnrollmentPurpose,
	finish func(challenge, binding []byte, opts ...grpc.CallOption) error,
) error {
	for attempt := 0; ; attempt++ {
		var began peer.Peer
		rsp, err := c.api.BeginEnrollment(ctx, &edgelinkv3.BeginEnrollmentRequest{Purpose: purpose}, grpc.Peer(&began))
		if err != nil {
			return fmt.Errorf("begin %s: %w", purpose, err)
		}
		cb, err := c.binding(&began)
		if err != nil {
			return err
		}
		var finished peer.Peer
		err = finish(rsp.GetChallenge(), cb, grpc.Peer(&finished))
		if err == nil || attempt > 0 {
			return err
		}
		if now, berr := c.binding(&finished); berr == nil && bytes.Equal(now, cb) {
			return err
		}
	}
}

// bearer sends the node credential on one call.
type bearer struct {
	token  string
	secure bool
}

func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

func (b bearer) RequireTransportSecurity() bool { return b.secure }

// EnrollOptions configure an enrollment.
type EnrollOptions struct {
	Dir       Dir
	Store     KeyStore
	ClaimCode string
	Platform  Platform
	// Assets are created in the claim's organization and bound to the node.
	Assets         []*edgelinkv3.EnrollingAsset
	DisplayName    string
	RuntimeVersion string
	Suite          uint32
	// NodeClass is the class the node is configured for; the claim must have the same.
	NodeClass edgelinkv3.NodeClass
	// Provisioning, when set, pins the installation and anchors the platform must answer with.
	Provisioning *Provisioning
	// Reenroll replaces an existing enrollment (a re-enrollment claim keeps the node id).
	Reenroll    bool
	DialOptions []grpc.DialOption
	Now         func() time.Time
}

// Enroll generates the identity key, proves possession of it with the claim code over this TLS
// connection, and stores the node id, the pinned anchors and the credential the platform returns.
// A failed enrollment leaves an existing one untouched.
func Enroll(ctx context.Context, o EnrollOptions) (*State, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if _, err := o.Dir.LoadState(); err == nil && !o.Reenroll {
		return nil, errors.New("identity: node is already enrolled; enroll again only with a re-enrollment claim code and --reenroll")
	}
	if handshake.NormalizeClaimCode(o.ClaimCode) == "" {
		return nil, errors.New("identity: claim code is required")
	}
	if !SupportedSuite(o.Suite) {
		return nil, fmt.Errorf("identity: suite %#04x is not supported", o.Suite)
	}
	if err := o.Store.Delete(KeyNext); err != nil {
		return nil, err
	}
	key, err := o.Store.Generate(KeyNext)
	if err != nil {
		return nil, err
	}
	state, credential, meta, err := enroll(ctx, o, key)
	if err != nil {
		return nil, errors.Join(err, o.Store.Delete(KeyNext))
	}
	if err := o.Dir.SaveState(state); err != nil {
		return nil, err
	}
	if err := o.Dir.SaveCredential(credential, meta); err != nil {
		return nil, err
	}
	if err := o.Store.Rename(KeyNext, KeyActive); err != nil {
		return nil, err
	}
	return state, nil
}

func enroll(ctx context.Context, o EnrollOptions, key Key) (*State, string, CredentialMeta, error) {
	var meta CredentialMeta
	pub, err := PublicKeyBytes(key)
	if err != nil {
		return nil, "", meta, err
	}
	keyID := handshake.KeyID(handshake.AlgECDSAP256, pub)
	conn, err := o.Platform.Dial(o.DialOptions...)
	if err != nil {
		return nil, "", meta, err
	}
	defer func() { _ = conn.Close() }()
	c := client{api: edgelinkv3.NewEdgeEnrollmentServiceClient(conn), insecure: o.Platform.Insecure}

	var rsp *edgelinkv3.CompleteEnrollmentResponse
	err = c.exchange(ctx, edgelinkv3.EnrollmentPurpose_ENROLLMENT_PURPOSE_ENROLL, func(challenge, cb []byte, opts ...grpc.CallOption) error {
		req := &edgelinkv3.CompleteEnrollmentRequest{
			ClaimCode:      o.ClaimCode,
			Challenge:      challenge,
			IdentityKey:    &edgelinkv3.IdentityKey{Alg: uint32(handshake.AlgECDSAP256), PublicKey: pub, Protection: key.Protection()},
			CryptoSuites:   []uint32{o.Suite},
			Assets:         o.Assets,
			RuntimeVersion: o.RuntimeVersion,
			DisplayName:    o.DisplayName,
			NodeClass:      o.NodeClass,
		}
		transcript, err := handshake.EnrollmentProofTranscript(req, cb)
		if err != nil {
			return err
		}
		if req.Signature, err = handshake.Sign(key, transcript); err != nil {
			return err
		}
		rsp, err = c.api.CompleteEnrollment(ctx, req, opts...)
		return err
	})
	if err != nil {
		return nil, "", meta, fmt.Errorf("identity: enrollment: %w", err)
	}

	state, err := stateFrom(rsp, o, keyID, key.Protection())
	if err != nil {
		return nil, "", meta, fmt.Errorf("identity: enrollment response refused: %w", err)
	}
	meta = CredentialMeta{IssuedAt: o.Now(), ExpiresAt: rsp.GetCredentialExpiresAt().AsTime()}
	return state, rsp.GetCredential(), meta, nil
}

func stateFrom(rsp *edgelinkv3.CompleteEnrollmentResponse, o EnrollOptions, keyID []byte, protection edgelinkv3.KeyProtection) (*State, error) {
	var errs []error
	if !bytes.Equal(rsp.GetIdentityKeyId(), keyID) {
		errs = append(errs, fmt.Errorf("identity key id %x, ours %x", rsp.GetIdentityKeyId(), keyID))
	}
	if rsp.GetNodeClass() != o.NodeClass {
		errs = append(errs, fmt.Errorf("node class %s, configured %s", rsp.GetNodeClass(), o.NodeClass))
	}
	if !slices.Contains(rsp.GetAllowedSuites(), o.Suite) {
		errs = append(errs, fmt.Errorf("suite %#04x is not allowed for this node (%v)", o.Suite, rsp.GetAllowedSuites()))
	}
	if rsp.GetCredential() == "" || rsp.GetCredentialExpiresAt() == nil {
		errs = append(errs, errors.New("no credential"))
	}
	anchors := make([]Anchor, 0, len(rsp.GetAnchors()))
	for i, a := range rsp.GetAnchors() {
		if _, err := handshake.ParsePublicKey(a.GetPublicKey()); err != nil || a.GetAlg() != uint32(handshake.AlgECDSAP256) ||
			!bytes.Equal(a.GetKeyId(), handshake.KeyID(handshake.AlgECDSAP256, a.GetPublicKey())) {
			errs = append(errs, fmt.Errorf("anchors[%d] is not a P-256 key matching its key id", i))
			continue
		}
		anchors = append(anchors, Anchor{KeyID: a.GetKeyId(), Alg: a.GetAlg(), PublicKey: a.GetPublicKey()})
	}
	if o.Provisioning != nil {
		if err := o.Provisioning.matchAnchors(rsp.GetInstallationId(), anchors); err != nil {
			errs = append(errs, fmt.Errorf("platform does not match the provisioning file: %w", err))
		}
	}
	class := ClassInfrastructure
	if rsp.GetNodeClass() == edgelinkv3.NodeClass_NODE_CLASS_PERSONAL {
		class = ClassPersonal
	}
	state := &State{
		Version:          1,
		NodeID:           rsp.GetNodeId(),
		OrganizationID:   rsp.GetOrganizationId(),
		OrganizationName: rsp.GetOrganizationName(),
		InstallationID:   rsp.GetInstallationId(),
		NodeClass:        class,
		Anchors:          anchors,
		AllowedSuites:    rsp.GetAllowedSuites(),
		Suite:            o.Suite,
		IdentityKeyID:    keyID,
		KeyProtection:    protection.String(),
		Platform:         o.Platform,
		EnrolledAt:       o.Now().UTC(),
	}
	if err := state.Validate(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return state, nil
}

// Node is an enrolled node's identity for the calls that need its credential.
type Node struct {
	Dir         Dir
	Store       KeyStore
	DialOptions []grpc.DialOption
	Now         func() time.Time
}

func (n Node) now() time.Time {
	if n.Now == nil {
		return time.Now()
	}
	return n.Now()
}

func (n Node) connect(st *State) (*grpc.ClientConn, client, bearer, error) {
	token, _, err := n.Dir.Credential()
	if err != nil {
		return nil, client{}, bearer{}, err
	}
	conn, err := st.Platform.Dial(n.DialOptions...)
	if err != nil {
		return nil, client{}, bearer{}, err
	}
	return conn, client{api: edgelinkv3.NewEdgeEnrollmentServiceClient(conn), insecure: st.Platform.Insecure},
		bearer{token: token, secure: !st.Platform.Insecure}, nil
}

// ErrRotationUnknown is returned when the rotation's outcome is not known. The new key is kept:
// the node offers it in its next Hello and the old key after a refusal.
var ErrRotationUnknown = errors.New("identity: rotation outcome unknown; the node tries the new key and then the old one")

// RotateKey replaces the identity key: the platform switches to a new key signed by both keys.
func (n Node) RotateKey(ctx context.Context) (*State, error) {
	st, err := n.Dir.LoadState()
	if err != nil {
		return nil, err
	}
	old, err := loadKey(n.Store, KeyActive)
	if err != nil {
		return nil, fmt.Errorf("identity: active key: %w", err)
	}
	if !bytes.Equal(keyID(old), st.IdentityKeyID) {
		return nil, errors.New("identity: a rotation is pending; start the node so it settles which key the platform has")
	}
	if err := n.Store.Delete(KeyNext); err != nil {
		return nil, err
	}
	next, err := n.Store.Generate(KeyNext)
	if err != nil {
		return nil, err
	}
	pub, err := PublicKeyBytes(next)
	if err != nil {
		return nil, err
	}
	nextID := handshake.KeyID(handshake.AlgECDSAP256, pub)
	conn, c, cred, err := n.connect(st)
	if err != nil {
		return nil, errors.Join(err, n.Store.Delete(KeyNext))
	}
	defer func() { _ = conn.Close() }()

	var rsp *edgelinkv3.RotateIdentityKeyResponse
	err = c.exchange(ctx, edgelinkv3.EnrollmentPurpose_ENROLLMENT_PURPOSE_ROTATE, func(challenge, cb []byte, opts ...grpc.CallOption) error {
		req := &edgelinkv3.RotateIdentityKeyRequest{
			Challenge: challenge,
			OldKeyId:  keyID(old),
			NewKey:    &edgelinkv3.IdentityKey{Alg: uint32(handshake.AlgECDSAP256), PublicKey: pub, Protection: next.Protection()},
		}
		transcript, err := handshake.RotateTranscript(st.OrganizationID, st.NodeID, req, cb)
		if err != nil {
			return err
		}
		if req.SignatureOld, err = handshake.Sign(old, transcript); err != nil {
			return err
		}
		if req.SignatureNew, err = handshake.Sign(next, transcript); err != nil {
			return err
		}
		rsp, err = c.api.RotateIdentityKey(ctx, req, append(opts, grpc.PerRPCCredentials(cred))...)
		return err
	})
	switch {
	case err != nil && outcomeUnknown(err):
		return nil, fmt.Errorf("%w: %v", ErrRotationUnknown, err)
	case err != nil:
		return nil, errors.Join(fmt.Errorf("identity: rotation refused: %w", err), n.Store.Delete(KeyNext))
	case !bytes.Equal(rsp.GetIdentityKeyId(), nextID):
		return nil, fmt.Errorf("%w: platform answered with key id %x", ErrRotationUnknown, rsp.GetIdentityKeyId())
	}
	updated := *st
	updated.IdentityKeyID = nextID
	updated.KeyProtection = next.Protection().String()
	if err := n.Dir.SaveState(&updated); err != nil {
		return nil, err
	}
	if err := n.Store.Rename(KeyNext, KeyActive); err != nil {
		return nil, err
	}
	return &updated, nil
}

// outcomeUnknown is true when the platform may have applied a call that returned err.
func outcomeUnknown(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Unknown, codes.Internal, codes.Aborted:
		return true
	}
	return false
}

// RenewCredential exchanges the credential for a new one, signed with the active identity key.
func (n Node) RenewCredential(ctx context.Context, signer *Provider) (CredentialMeta, error) {
	st := signer.State()
	conn, c, cred, err := n.connect(st)
	if err != nil {
		return CredentialMeta{}, err
	}
	defer func() { _ = conn.Close() }()
	var rsp *edgelinkv3.RenewCredentialResponse
	err = c.exchange(ctx, edgelinkv3.EnrollmentPurpose_ENROLLMENT_PURPOSE_RENEW, func(challenge, cb []byte, opts ...grpc.CallOption) error {
		req := &edgelinkv3.RenewCredentialRequest{Challenge: challenge, KeyId: signer.KeyID()}
		var err error
		if req.Signature, err = signer.Sign(req.KeyId, handshake.RenewTranscript(st.OrganizationID, st.NodeID, req, cb)); err != nil {
			return err
		}
		rsp, err = c.api.RenewCredential(ctx, req, append(opts, grpc.PerRPCCredentials(cred))...)
		return err
	})
	if err != nil {
		return CredentialMeta{}, fmt.Errorf("identity: credential renewal: %w", err)
	}
	if rsp.GetCredential() == "" || rsp.GetCredentialExpiresAt() == nil {
		return CredentialMeta{}, errors.New("identity: credential renewal: no credential in the answer")
	}
	meta := CredentialMeta{IssuedAt: n.now(), ExpiresAt: rsp.GetCredentialExpiresAt().AsTime()}
	if err := n.Dir.SaveCredential(rsp.GetCredential(), meta); err != nil {
		return CredentialMeta{}, err
	}
	return meta, nil
}

// RunRenewal renews the credential whenever a third of its lifetime remains, retrying failures
// with backoff (1 min to 1 h), until ctx ends.
func (n Node) RunRenewal(ctx context.Context, signer *Provider, log *slog.Logger) {
	retry := time.Minute
	for {
		_, meta, err := n.Dir.Credential()
		wait := time.Hour
		if err == nil {
			wait = meta.RenewAt().Sub(n.now())
		} else {
			log.Warn("credential unreadable", "error", err)
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		meta, err = n.RenewCredential(ctx, signer)
		if err == nil {
			log.Info("credential renewed", "expires_at", meta.ExpiresAt)
			retry = time.Minute
			continue
		}
		if ctx.Err() != nil {
			return
		}
		log.Warn("credential renewal failed", "error", err, "retry_in", retry)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry/2 + rand.N(retry/2+1)):
		}
		retry = min(retry*2, time.Hour)
	}
}
