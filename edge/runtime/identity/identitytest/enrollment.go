package identitytest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tlsbinding"
)

// EnrolledNode is a node the fake platform enrolled.
type EnrolledNode struct {
	NodeID     string
	KeyID      []byte
	PublicKey  []byte
	Protection edgelinkv3.KeyProtection
	Assets     []string
	Credential string
	// Binding is the channel binding the platform saw for the proof.
	Binding []byte
}

// EnrollmentServer is an EdgeEnrollmentService that verifies proofs with the channel binding of
// its own TLS session, as edge-link and connector do. Every refusal answers alike; a node class
// other than the claim's is answered with FailedPrecondition before a node is created.
type EnrollmentServer struct {
	edgelinkv3.UnimplementedEdgeEnrollmentServiceServer
	Authority      *Authority
	OrganizationID string
	ClaimCode      string
	NodeClass      edgelinkv3.NodeClass
	Lifetime       time.Duration
	// Addr is where the server listens.
	Addr string

	mu         sync.Mutex
	challenges map[string]edgelinkv3.EnrollmentPurpose
	nodes      map[string]*EnrolledNode
	failErr    error
}

// StartEnrollment serves an EnrollmentServer on localhost; tls nil serves without TLS.
func StartEnrollment(t testing.TB, a *Authority, tls *TLS) *EnrollmentServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var opts []grpc.ServerOption
	if tls != nil {
		opts = append(opts, grpc.Creds(tls.Server))
	}
	srv := grpc.NewServer(opts...)
	s := &EnrollmentServer{
		Authority:      a,
		OrganizationID: uuid.NewString(),
		ClaimCode:      "ZQ-ABCDE-FGHJK",
		NodeClass:      edgelinkv3.NodeClass_NODE_CLASS_INFRASTRUCTURE,
		Lifetime:       365 * 24 * time.Hour,
		Addr:           lis.Addr().String(),
		challenges:     map[string]edgelinkv3.EnrollmentPurpose{},
		nodes:          map[string]*EnrolledNode{},
	}
	edgelinkv3.RegisterEdgeEnrollmentServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return s
}

var errRefused = status.Error(codes.Unauthenticated, "enrollment refused")

// Nodes are the enrolled nodes.
func (s *EnrollmentServer) Nodes() []*EnrolledNode {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*EnrolledNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		c := *n
		out = append(out, &c)
	}
	return out
}

func binding(ctx context.Context) []byte {
	p, _ := peer.FromContext(ctx)
	cb, err := tlsbinding.FromPeer(p)
	if err != nil {
		return nil
	}
	return cb
}

func (s *EnrollmentServer) consume(challenge []byte, purpose edgelinkv3.EnrollmentPurpose) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hex.EncodeToString(challenge)
	got, ok := s.challenges[key]
	delete(s.challenges, key)
	return ok && got == purpose
}

// FailNext makes the next CompleteEnrollment, RotateIdentityKey or RenewCredential fail with err
// after it took effect, as when the answer is lost.
func (s *EnrollmentServer) FailNext(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failErr = err
}

func (s *EnrollmentServer) failNext() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.failErr
	s.failErr = nil
	return err
}

func (s *EnrollmentServer) credential() (string, *timestamppb.Timestamp) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "cred-" + hex.EncodeToString(b), timestamppb.New(time.Now().Add(s.Lifetime))
}

func (s *EnrollmentServer) BeginEnrollment(_ context.Context, req *edgelinkv3.BeginEnrollmentRequest) (*edgelinkv3.BeginEnrollmentResponse, error) {
	challenge := make([]byte, 32)
	_, _ = rand.Read(challenge)
	s.mu.Lock()
	s.challenges[hex.EncodeToString(challenge)] = req.GetPurpose()
	s.mu.Unlock()
	return &edgelinkv3.BeginEnrollmentResponse{Challenge: challenge, ExpiresAt: timestamppb.New(time.Now().Add(5 * time.Minute))}, nil
}

func (s *EnrollmentServer) CompleteEnrollment(ctx context.Context, req *edgelinkv3.CompleteEnrollmentRequest) (*edgelinkv3.CompleteEnrollmentResponse, error) {
	cb := binding(ctx)
	if !s.consume(req.GetChallenge(), edgelinkv3.EnrollmentPurpose_ENROLLMENT_PURPOSE_ENROLL) ||
		handshake.NormalizeClaimCode(req.GetClaimCode()) != handshake.NormalizeClaimCode(s.ClaimCode) {
		return nil, errRefused
	}
	transcript, err := handshake.EnrollmentProofTranscript(req, cb)
	if err != nil || handshake.Verify(uint8(req.GetIdentityKey().GetAlg()), req.GetIdentityKey().GetPublicKey(), transcript, req.GetSignature()) != nil {
		return nil, errRefused
	}
	if req.GetNodeClass() != edgelinkv3.NodeClass_NODE_CLASS_UNSPECIFIED && req.GetNodeClass() != s.NodeClass {
		return nil, status.Errorf(codes.FailedPrecondition, "this code enrolls a %s node; this node is configured as %s",
			s.NodeClass, req.GetNodeClass())
	}
	keyID := handshake.KeyID(handshake.AlgECDSAP256, req.GetIdentityKey().GetPublicKey())
	token, expires := s.credential()
	n := &EnrolledNode{
		NodeID:     uuid.NewString(),
		KeyID:      keyID,
		PublicKey:  req.GetIdentityKey().GetPublicKey(),
		Protection: req.GetIdentityKey().GetProtection(),
		Credential: token,
		Binding:    cb,
	}
	for _, a := range req.GetAssets() {
		n.Assets = append(n.Assets, a.GetSn())
	}
	s.mu.Lock()
	s.nodes[n.NodeID] = n
	s.mu.Unlock()
	if err := s.failNext(); err != nil {
		return nil, err
	}
	return &edgelinkv3.CompleteEnrollmentResponse{
		NodeId:              n.NodeID,
		OrganizationId:      s.OrganizationID,
		OrganizationName:    "Test Org",
		InstallationId:      s.Authority.InstallationID,
		Anchors:             s.Authority.TrustAnchors(),
		Credential:          token,
		CredentialExpiresAt: expires,
		IdentityKeyId:       keyID,
		AllowedSuites:       []uint32{0x0101, 0x0102},
		NodeClass:           s.NodeClass,
	}, nil
}

func (s *EnrollmentServer) caller(ctx context.Context) (*EnrolledNode, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	auth := md.Get("authorization")
	if len(auth) != 1 {
		return nil, errRefused
	}
	token := strings.TrimPrefix(auth[0], "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.nodes {
		if n.Credential == token {
			return n, nil
		}
	}
	return nil, errRefused
}

func (s *EnrollmentServer) RotateIdentityKey(ctx context.Context, req *edgelinkv3.RotateIdentityKeyRequest) (*edgelinkv3.RotateIdentityKeyResponse, error) {
	n, err := s.caller(ctx)
	if err != nil || !s.consume(req.GetChallenge(), edgelinkv3.EnrollmentPurpose_ENROLLMENT_PURPOSE_ROTATE) ||
		!bytes.Equal(req.GetOldKeyId(), n.KeyID) {
		return nil, errRefused
	}
	transcript, err := handshake.RotateTranscript(s.OrganizationID, n.NodeID, req, binding(ctx))
	if err != nil ||
		handshake.Verify(handshake.AlgECDSAP256, n.PublicKey, transcript, req.GetSignatureOld()) != nil ||
		handshake.Verify(uint8(req.GetNewKey().GetAlg()), req.GetNewKey().GetPublicKey(), transcript, req.GetSignatureNew()) != nil {
		return nil, errRefused
	}
	newID := handshake.KeyID(handshake.AlgECDSAP256, req.GetNewKey().GetPublicKey())
	s.mu.Lock()
	n.KeyID, n.PublicKey, n.Protection = newID, req.GetNewKey().GetPublicKey(), req.GetNewKey().GetProtection()
	s.mu.Unlock()
	if err := s.failNext(); err != nil {
		return nil, err
	}
	return &edgelinkv3.RotateIdentityKeyResponse{IdentityKeyId: newID}, nil
}

func (s *EnrollmentServer) RenewCredential(ctx context.Context, req *edgelinkv3.RenewCredentialRequest) (*edgelinkv3.RenewCredentialResponse, error) {
	n, err := s.caller(ctx)
	if err != nil || !s.consume(req.GetChallenge(), edgelinkv3.EnrollmentPurpose_ENROLLMENT_PURPOSE_RENEW) ||
		!bytes.Equal(req.GetKeyId(), n.KeyID) {
		return nil, errRefused
	}
	transcript := handshake.RenewTranscript(s.OrganizationID, n.NodeID, req, binding(ctx))
	if handshake.Verify(handshake.AlgECDSAP256, n.PublicKey, transcript, req.GetSignature()) != nil {
		return nil, errRefused
	}
	token, expires := s.credential()
	s.mu.Lock()
	n.Credential = token
	s.mu.Unlock()
	if err := s.failNext(); err != nil {
		return nil, err
	}
	return &edgelinkv3.RenewCredentialResponse{Credential: token, CredentialExpiresAt: expires}, nil
}

// ErrUnavailable is a transport failure after the platform applied a call.
var ErrUnavailable = status.Error(codes.Unavailable, "connection lost")
