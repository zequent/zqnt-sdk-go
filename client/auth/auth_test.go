package auth_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/Zequent/zqnt-client-sdk-go/auth"
	"github.com/Zequent/zqnt-client-sdk-go/connector"
	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// recorder is a real gRPC server on a free port that records the authorization header of every
// call and, when told to, refuses it like core does.
type recorder struct {
	connectorpb.UnimplementedConnectorServiceServer
	mu      sync.Mutex
	headers []string
	refuse  error
}

func (r *recorder) record(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(values) == 0 {
		r.headers = append(r.headers, "<none>")
	} else {
		r.headers = append(r.headers, strings.Join(values, ","))
	}
	return r.refuse
}

func (r *recorder) GetAssetBySn(ctx context.Context, req *base.RequestBase) (*connectorpb.ConnectorResponse, error) {
	if err := r.record(ctx); err != nil {
		return nil, err
	}
	return &connectorpb.ConnectorResponse{}, nil
}

func (r *recorder) AssetMonitoring(req *base.RequestBase, stream grpc.ServerStreamingServer[connectorpb.AssetMonitoringResponse]) error {
	if err := r.record(stream.Context()); err != nil {
		return err
	}
	return stream.Send(&connectorpb.AssetMonitoringResponse{})
}

func serve(t *testing.T, r *recorder, token string) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	connectorpb.RegisterConnectorServiceServer(server, r)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	opts := append(auth.DialOptions(token), grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(lis.Addr().String(), opts...)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestEveryCallCarriesTheCredentialAsABearerToken(t *testing.T) {
	t.Setenv(auth.EnvVar, "")
	r := &recorder{}
	client := connector.New(serve(t, r, "tok-123"))
	for i := 0; i < 2; i++ {
		if _, err := client.GetAssetBySn(context.Background(), "DOCK-1"); err != nil {
			t.Fatalf("GetAssetBySn: %v", err)
		}
	}
	if got := strings.Join(r.headers, "|"); got != "Bearer tok-123|Bearer tok-123" {
		t.Fatalf("headers = %q", got)
	}
}

func TestAStreamCarriesTheCredentialToo(t *testing.T) {
	t.Setenv(auth.EnvVar, "")
	r := &recorder{}
	stream, err := connectorpb.NewConnectorServiceClient(serve(t, r, "tok-stream")).
		AssetMonitoring(context.Background(), &base.RequestBase{Sn: "DOCK-1"})
	if err != nil {
		t.Fatalf("AssetMonitoring: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if r.headers[0] != "Bearer tok-stream" {
		t.Fatalf("headers = %v", r.headers)
	}
}

func TestTheEnvironmentVariableIsUsedWhenNoTokenIsPassed(t *testing.T) {
	t.Setenv(auth.EnvVar, "  from-env ")
	r := &recorder{}
	if _, err := connector.New(serve(t, r, "")).GetAssetBySn(context.Background(), "DOCK-1"); err != nil {
		t.Fatalf("GetAssetBySn: %v", err)
	}
	if r.headers[0] != "Bearer from-env" {
		t.Fatalf("headers = %v", r.headers)
	}
	if auth.Token("explicit") != "explicit" {
		t.Fatal("an explicit token wins over the environment")
	}
}

func TestWithoutACredentialNothingIsSentAndTheRefusalNamesTheVariable(t *testing.T) {
	t.Setenv(auth.EnvVar, "")
	r := &recorder{refuse: status.Error(codes.Unauthenticated, "Authentication required")}
	_, err := connector.New(serve(t, r, "")).GetAssetBySn(context.Background(), "DOCK-1")
	if status.Code(errorsUnwrap(err)) != codes.Unauthenticated {
		t.Fatalf("code = %v (%v)", status.Code(errorsUnwrap(err)), err)
	}
	if !strings.Contains(err.Error(), auth.EnvVar) || !strings.Contains(err.Error(), "Authentication required") {
		t.Fatalf("message does not say what to do: %v", err)
	}
	if r.headers[0] != "<none>" {
		t.Fatalf("headers = %v", r.headers)
	}
}

func TestARefusedStreamIsExplained(t *testing.T) {
	t.Setenv(auth.EnvVar, "")
	r := &recorder{refuse: status.Error(codes.PermissionDenied, "Asset X is not one of this credential's organization's assets")}
	stream, err := connectorpb.NewConnectorServiceClient(serve(t, r, "tok")).
		AssetMonitoring(context.Background(), &base.RequestBase{Sn: "X"})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "own organization") {
		t.Fatalf("stream error not explained: %v", err)
	}
}

func TestExplainLeavesOtherErrorsAlone(t *testing.T) {
	original := status.Error(codes.Unavailable, "down")
	if auth.Explain(original) != original {
		t.Fatal("UNAVAILABLE must pass through unchanged")
	}
	if auth.Explain(nil) != nil {
		t.Fatal("nil stays nil")
	}
}

// errorsUnwrap digs the gRPC status out of the connector client's fmt.Errorf wrapping.
func errorsUnwrap(err error) error {
	for err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
	return err
}

func TestAPerCallTokenWinsOverTheConnectionsCredential(t *testing.T) {
	t.Setenv(auth.EnvVar, "")
	r := &recorder{}
	client := connector.New(serve(t, r, "fixed-token"))
	if _, err := client.GetAssetBySn(auth.WithToken(context.Background(), "forwarded"), "DOCK-1"); err != nil {
		t.Fatalf("GetAssetBySn: %v", err)
	}
	if _, err := client.GetAssetBySn(auth.WithToken(context.Background(), "  "), "DOCK-1"); err != nil {
		t.Fatalf("GetAssetBySn: %v", err)
	}
	if got := strings.Join(r.headers, "|"); got != "Bearer forwarded|Bearer fixed-token" {
		t.Fatalf("headers = %q (a per-call token is the only one; a blank one changes nothing)", got)
	}
}

func TestAHostInterceptorRegisteredFirstWinsOnUnaryAndStreamingCalls(t *testing.T) {
	t.Setenv(auth.EnvVar, "")
	r := &recorder{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	connectorpb.RegisterConnectorServiceServer(server, r)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	unary := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(auth.WithToken(ctx, "caller-token"), method, req, reply, cc, opts...)
	}
	stream := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(auth.WithToken(ctx, "caller-token"), desc, cc, method, opts...)
	}
	opts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(unary),
		grpc.WithChainStreamInterceptor(stream),
	}, auth.DialOptions("fixed-token")...)
	conn, err := grpc.NewClient(lis.Addr().String(), opts...)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := connector.New(conn).GetAssetBySn(context.Background(), "DOCK-1"); err != nil {
		t.Fatalf("GetAssetBySn: %v", err)
	}
	s, err := connectorpb.NewConnectorServiceClient(conn).AssetMonitoring(context.Background(), &base.RequestBase{Sn: "DOCK-1"})
	if err != nil {
		t.Fatalf("AssetMonitoring: %v", err)
	}
	if _, err := s.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got := strings.Join(r.headers, "|"); got != "Bearer caller-token|Bearer caller-token" {
		t.Fatalf("headers = %q", got)
	}
}
