package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	commonpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/proto"
	edgepb "github.com/zequent/zqnt-utils-golang/v2/gen/edge/sdk/proto"
)

func newKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(der)
}

type tokenOpts struct {
	aud, iss, alg string
	scope         []string
	ttl           time.Duration
}

// sign mints a token the way core's ServiceTokenCodec does.
func sign(t *testing.T, key ed25519.PrivateKey, o tokenOpts) string {
	t.Helper()
	if o.aud == "" {
		o.aud = AudienceEdge
	}
	if o.iss == "" {
		o.iss = Issuer
	}
	if o.alg == "" {
		o.alg = "EdDSA"
	}
	if o.scope == nil {
		o.scope = []string{ScopePlatform}
	}
	if o.ttl == 0 {
		o.ttl = 5 * time.Minute
	}
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": o.alg, "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"iss": o.iss, "sub": "svc:remote-control-service", "aud": o.aud, "scope": o.scope,
		"iat": now.Unix(), "exp": now.Add(o.ttl).Unix(), "jti": "j",
	})
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(header) + "." + enc.EncodeToString(payload)
	return input + "." + enc.EncodeToString(ed25519.Sign(key, []byte(input)))
}

func TestVerifierAcceptsThePlatformsToken(t *testing.T) {
	key, pub := newKey(t)
	v, err := NewVerifier(pub)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := v.Verify(sign(t, key, tokenOpts{}))
	if err != nil || sub != "svc:remote-control-service" {
		t.Fatalf("got %q, %v", sub, err)
	}
}

func TestVerifierRefusesEverythingElse(t *testing.T) {
	key, pub := newKey(t)
	other, _ := newKey(t)
	v, _ := NewVerifier(pub)
	cases := map[string]string{
		"another key":        sign(t, other, tokenOpts{}),
		"meant for core":     sign(t, key, tokenOpts{aud: "zqnt-platform"}),
		"an edge credential": sign(t, key, tokenOpts{scope: []string{"edge"}}),
		"a user token":       sign(t, key, tokenOpts{iss: "zqnt-admin-console"}),
		"expired":            sign(t, key, tokenOpts{ttl: -2 * time.Minute}),
		"alg none":           sign(t, key, tokenOpts{alg: "none"}),
		"garbage":            "a.b.c",
		"not a jws":          "abc",
	}
	for name, token := range cases {
		if _, err := v.Verify(token); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestABadKeyIsAConfigurationError(t *testing.T) {
	if _, err := NewGuard(Config{PlatformPublicKey: "not-a-key"}, nil); err == nil {
		t.Fatal("expected an error")
	}
}

func withBearer(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func TestGuard(t *testing.T) {
	key, pub := newKey(t)
	guard, _ := NewGuard(Config{PlatformPublicKey: pub}, nil)
	const method = "/zqnt.EdgeAdapterService/TakeOff"

	if status.Code(guard.Check(context.Background(), method)) != codes.Unauthenticated {
		t.Error("a command without a token passed")
	}
	if err := guard.Check(withBearer(sign(t, key, tokenOpts{})), method); err != nil {
		t.Errorf("the platform's token was refused: %v", err)
	}
	if err := guard.Check(context.Background(), "/grpc.health.v1.Health/Check"); err != nil {
		t.Errorf("health probes need no token: %v", err)
	}

	unconfigured, _ := NewGuard(Config{}, nil)
	if status.Code(unconfigured.Check(withBearer(sign(t, key, tokenOpts{})), method)) != codes.Unauthenticated {
		t.Error("without a public key every command must be refused")
	}
	disabled, _ := NewGuard(Config{Disabled: true}, nil)
	if err := disabled.Check(context.Background(), method); err != nil {
		t.Errorf("the dev switch must let everything through: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("ZQNT_EDGE_TOKEN", "edge-token")
	t.Setenv("ZQNT_PLATFORM_PUBLIC_KEY", "")
	t.Setenv("SERVICE_AUTH_PUBLIC_KEY", "alias-key")
	t.Setenv("ZQNT_EDGE_AUTH_DISABLED", "maybe")
	cfg := FromEnv()
	if cfg.EdgeToken != "edge-token" || cfg.PlatformPublicKey != "alias-key" || cfg.Disabled {
		t.Fatalf("got %+v", cfg)
	}
	t.Setenv("ZQNT_EDGE_AUTH_DISABLED", "true")
	if !FromEnv().Disabled {
		t.Fatal("ZQNT_EDGE_AUTH_DISABLED=true must disable the check")
	}
}

// A real server with the interceptors installed, and a client using BearerCredentials.
func TestEndToEnd(t *testing.T) {
	key, pub := newKey(t)
	guard, _ := NewGuard(Config{PlatformPublicKey: pub}, nil)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(guard.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(guard.StreamServerInterceptor()))
	// Every EdgeAdapterService method answers Unimplemented here: reaching it means the guard let
	// the call through.
	healthpb.RegisterHealthServer(srv, health.NewServer())
	edgepb.RegisterEdgeAdapterServiceServer(srv, edgepb.UnimplementedEdgeAdapterServiceServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dial := func(opts ...grpc.DialOption) *grpc.ClientConn {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
		conn, err := grpc.NewClient("passthrough:///bufnet", opts...)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}

	// Health stays open without a token.
	open := dial()
	defer open.Close()
	if _, err := healthpb.NewHealthClient(open).Check(context.Background(), &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health: %v", err)
	}

	ctx := context.Background()
	request := &commonpb.AssetCapabilitiesRequest{Sn: "TEST-001"}
	_, err := edgepb.NewEdgeAdapterServiceClient(open).GetCapabilities(ctx, request)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("a command without a token: got %v", err)
	}

	signed := dial(grpc.WithPerRPCCredentials(BearerCredentials{Token: sign(t, key, tokenOpts{})}))
	defer signed.Close()
	_, err = edgepb.NewEdgeAdapterServiceClient(signed).GetCapabilities(ctx, request)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("a signed command must get past the guard (to Unimplemented here): got %v", err)
	}
}
