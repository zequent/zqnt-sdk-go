// Package auth authenticates both directions of an edge adapter's gRPC traffic (2026-09-30
// security review, GRPC-1 / SAST-3).
//
//   - Adapter -> platform: core refuses calls without a credential. The adapter is configured with
//     an edge credential (ZQNT_EDGE_TOKEN) issued in the console (POST
//     /api/admin-console/edge-credentials) or by core/scripts/mint-edge-credential.py; every
//     connection to connector / live-data / mission-autonomy attaches it ([BearerCredentials]).
//
//   - Platform -> adapter: the platform signs a short-lived service token (audience zqnt-edge) for
//     every command. The adapter's server verifies it with the platform's published signing keys
//     (ZQNT_PLATFORM_KEYS_URL, core's JWK set; the token's kid names the key, see [KeySet]) and/or
//     the static service public key (ZQNT_PLATFORM_PUBLIC_KEY, alias SERVICE_AUTH_PUBLIC_KEY, for
//     tokens without kid), and refuses everything else ([UnaryServerInterceptor],
//     [StreamServerInterceptor]).
//
// ZQNT_EDGE_AUTH_DISABLED=true turns the inbound check off -- local SITL/simulators only. Without a
// key set URL, without a public key and without that switch every command is refused (fail closed).
//
// Token contract (must match core's ServiceTokens.java): compact JWS, alg EdDSA (Ed25519), optional
// kid "<key>:v<n>", iss "zqnt-service", aud "zqnt-edge", scope contains "platform", exp in the future.
package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	Issuer        = "zqnt-service"
	AudienceEdge  = "zqnt-edge"
	ScopePlatform = "platform"
	clockSkew     = 30 * time.Second
)

// Config holds an adapter's authentication settings.
type Config struct {
	// EdgeToken is the credential for calls INTO the platform (ZQNT_EDGE_TOKEN).
	EdgeToken string
	// PlatformPublicKey verifies the platform's commands (ZQNT_PLATFORM_PUBLIC_KEY or
	// SERVICE_AUTH_PUBLIC_KEY): base64 DER (SubjectPublicKeyInfo) or PEM.
	PlatformPublicKey string
	// PlatformKeysURL is where the platform publishes its signing keys as a JWK set
	// (ZQNT_PLATFORM_KEYS_URL, e.g. http://admin-console:8005/api/admin-console/auth/service-keys).
	PlatformKeysURL string
	// Disabled skips the inbound check (ZQNT_EDGE_AUTH_DISABLED=true). Local SITL only.
	Disabled bool
}

// FromEnv reads the Config from the environment.
func FromEnv() Config {
	key := os.Getenv("ZQNT_PLATFORM_PUBLIC_KEY")
	if key == "" {
		key = os.Getenv("SERVICE_AUTH_PUBLIC_KEY")
	}
	cfg := Config{
		EdgeToken:         os.Getenv("ZQNT_EDGE_TOKEN"),
		PlatformPublicKey: key,
		PlatformKeysURL:   strings.TrimSpace(os.Getenv("ZQNT_PLATFORM_KEYS_URL")),
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZQNT_EDGE_AUTH_DISABLED"))) {
	case "1", "true", "yes", "on":
		cfg.Disabled = true
	}
	return cfg
}

// Verifier checks the service token the platform puts on every call into the adapter.
type Verifier struct {
	keys Keys
	now  func() time.Time
}

// NewVerifier verifies tokens without kid with the platform's Ed25519 public key.
func NewVerifier(publicKey string) (*Verifier, error) {
	key, err := ParsePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	return NewKeysVerifier(StaticKey{key: key}), nil
}

// NewKeysVerifier verifies tokens with the keys keys resolves, typically a [KeySet].
func NewKeysVerifier(keys Keys) *Verifier {
	return &Verifier{keys: keys, now: time.Now}
}

// Claims is what a verified platform token asserts.
type Claims struct {
	Subject        string
	OrganizationID string
	Scope          []string
}

type claims struct {
	Iss   string   `json:"iss"`
	Sub   string   `json:"sub"`
	Aud   string   `json:"aud"`
	Scope []string `json:"scope"`
	OrgID string   `json:"org_id"`
	Exp   *int64   `json:"exp"`
	Iat   *int64   `json:"iat"`
}

// Verify returns what the token asserts, or why it is refused.
func (v *Verifier) Verify(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("token must be a compact JWS")
	}
	decode := base64.RawURLEncoding.DecodeString
	headerJSON, err := decode(parts[0])
	if err != nil {
		return Claims{}, errors.New("token is not well-formed")
	}
	payloadJSON, err := decode(parts[1])
	if err != nil {
		return Claims{}, errors.New("token is not well-formed")
	}
	signature, err := decode(parts[2])
	if err != nil {
		return Claims{}, errors.New("token is not well-formed")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headerJSON, &header) != nil || header.Alg != "EdDSA" {
		return Claims{}, errors.New("only EdDSA tokens are accepted")
	}
	key, err := v.keys.Key(header.Kid)
	if err != nil {
		return Claims{}, err
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, errors.New("token signature is invalid")
	}
	var c claims
	if json.Unmarshal(payloadJSON, &c) != nil {
		return Claims{}, errors.New("token is not well-formed")
	}
	if c.Iss != Issuer {
		return Claims{}, errors.New("unexpected token issuer")
	}
	if c.Aud != AudienceEdge {
		return Claims{}, errors.New("token is not meant for an edge adapter")
	}
	platform := false
	for _, s := range c.Scope {
		platform = platform || s == ScopePlatform
	}
	if !platform {
		return Claims{}, errors.New("token is not a platform service token")
	}
	now := v.now()
	if c.Exp == nil || time.Unix(*c.Exp, 0).Add(clockSkew).Before(now) {
		return Claims{}, errors.New("token is expired")
	}
	if c.Iat != nil && time.Unix(*c.Iat, 0).Add(-clockSkew).After(now) {
		return Claims{}, errors.New("token is issued in the future")
	}
	return Claims{Subject: c.Sub, OrganizationID: c.OrgID, Scope: c.Scope}, nil
}

// Guard decides whether an inbound call may proceed.
type Guard struct {
	disabled      bool
	verifier      *Verifier
	keySet        *KeySet
	misconfigured string
	log           *slog.Logger
}

// NewGuard builds the inbound check from cfg. Missing keys are not an error: the guard then
// refuses every call and says why, so a misconfigured adapter fails closed and visibly. With a key
// set URL the guard loads the platform's keys in the background; [Guard.Close] stops that.
func NewGuard(cfg Config, log *slog.Logger) (*Guard, error) {
	if log == nil {
		log = slog.Default()
	}
	g := &Guard{disabled: cfg.Disabled, log: log}
	switch {
	case cfg.Disabled:
		log.Warn("ZQNT_EDGE_AUTH_DISABLED is set: this adapter accepts commands from ANYONE who can reach " +
			"its port. Local SITL/simulator use only.")
	case cfg.PlatformKeysURL == "" && cfg.PlatformPublicKey == "":
		g.misconfigured = "neither ZQNT_PLATFORM_KEYS_URL nor ZQNT_PLATFORM_PUBLIC_KEY is configured"
		log.Error("ZQNT_PLATFORM_KEYS_URL and ZQNT_PLATFORM_PUBLIC_KEY are not set: every platform command will " +
			"be refused. Set ZQNT_PLATFORM_KEYS_URL to the platform's /api/admin-console/auth/service-keys " +
			"and/or ZQNT_PLATFORM_PUBLIC_KEY to its SERVICE_AUTH_PUBLIC_KEY (or ZQNT_EDGE_AUTH_DISABLED=true for local SITL).")
	case cfg.PlatformKeysURL == "":
		v, err := NewVerifier(cfg.PlatformPublicKey)
		if err != nil {
			return nil, err
		}
		g.verifier = v
	default:
		var static ed25519.PublicKey
		if cfg.PlatformPublicKey != "" {
			key, err := ParsePublicKey(cfg.PlatformPublicKey)
			if err != nil {
				return nil, err
			}
			static = key
		}
		g.keySet = NewKeySet(cfg.PlatformKeysURL, static, log)
		g.keySet.Start()
		g.verifier = NewKeysVerifier(g.keySet)
	}
	return g, nil
}

// Close stops the background refresh of the platform's keys.
func (g *Guard) Close() {
	if g.keySet != nil {
		g.keySet.Close()
	}
}

// Check returns nil when the call may proceed, else a gRPC status error.
func (g *Guard) Check(ctx context.Context, fullMethod string) error {
	if g.disabled || strings.HasPrefix(fullMethod, "/grpc.health.v1.Health/") {
		return nil
	}
	if g.verifier == nil {
		return status.Error(codes.Unauthenticated, g.misconfigured)
	}
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) == 0 || !strings.HasPrefix(values[0], "Bearer ") {
		return status.Error(codes.Unauthenticated, "authentication required")
	}
	if _, err := g.verifier.Verify(strings.TrimPrefix(values[0], "Bearer ")); err != nil {
		g.log.Warn("refused platform command", "method", fullMethod, "reason", err.Error())
		return status.Error(codes.Unauthenticated, err.Error())
	}
	return nil
}

// UnaryServerInterceptor refuses unary calls the guard does not let through.
func (g *Guard) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := g.Check(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor refuses streaming calls the guard does not let through.
func (g *Guard) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := g.Check(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// BearerCredentials attaches the adapter's edge credential to every call into the platform.
type BearerCredentials struct {
	Token string
}

// GetRequestMetadata implements credentials.PerRPCCredentials.
func (b BearerCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.Token}, nil
}

// RequireTransportSecurity implements credentials.PerRPCCredentials. False: the platform's
// internal channels are plaintext today (internal TLS is a separate item of the review).
func (BearerCredentials) RequireTransportSecurity() bool { return false }
