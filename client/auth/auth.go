// Package auth attaches the client credential a customer application calls the Zequent platform
// with, and turns the platform's refusals into errors that say what to do.
//
// Every core service refuses a call that carries no credential. An organization administrator
// issues one in the console (Deploy -> Access & Integrations -> Credentials, kind "client"); it is
// shown once, acts for that one organization, and reaches only that organization's assets,
// Applications and runs.
//
// The SDK's clients wrap a connection you dial yourself, so the credential is a dial option:
//
//	opts := append(auth.DialOptions(""), // "" = read ZQNT_CLIENT_TOKEN
//		grpc.WithTransportCredentials(insecure.NewCredentials()))
//	conn, err := grpc.NewClient("core.example.com:8010", opts...)
//	assets := connector.New(conn)
//
// It is sent as "authorization: Bearer <token>" on every call, unary and streaming.
package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EnvVar is read when no token is passed explicitly.
const EnvVar = "ZQNT_CLIENT_TOKEN"

// Token returns explicit if it is not blank, else the value of ZQNT_CLIENT_TOKEN; "" when neither
// is set.
func Token(explicit string) string {
	if t := strings.TrimSpace(explicit); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv(EnvVar))
}

// Credentials is a credentials.PerRPCCredentials sending a client credential as a bearer token,
// for code that prefers grpc.WithPerRPCCredentials over DialOptions (it does not explain refusals;
// wrap errors with Explain). Use one or the other, not both.
type Credentials struct {
	token string
}

// NewCredentials returns the per-RPC credentials for token (resolved with Token).
func NewCredentials(token string) Credentials {
	return Credentials{token: Token(token)}
}

// GetRequestMetadata implements credentials.PerRPCCredentials.
func (c Credentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	if c.token == "" {
		return nil, nil
	}
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}

// RequireTransportSecurity implements credentials.PerRPCCredentials. False: the platform's gRPC
// ports are commonly reached in plaintext inside a private network; use TLS (and a
// transport-credential dial option) whenever the token crosses one you do not control.
func (Credentials) RequireTransportSecurity() bool { return false }

// DialOptions returns the dial options that send the client credential on every call, unary and
// streaming, and explain refusals. An empty token reads ZQNT_CLIENT_TOKEN; with neither, no header
// is sent and the platform's refusal comes back explained.
func DialOptions(token string) []grpc.DialOption {
	c := NewCredentials(token)
	return []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(unaryInterceptor(c)),
		grpc.WithChainStreamInterceptor(streamInterceptor(c)),
	}
}

func withBearer(ctx context.Context, c Credentials) context.Context {
	if c.token == "" {
		return ctx
	}
	return withAuthorization(ctx, "Bearer "+c.token)
}

func unaryInterceptor(c Credentials) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return Explain(invoker(withBearer(ctx, c), method, req, reply, cc, opts...))
	}
}

func streamInterceptor(c Credentials) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		stream, err := streamer(withBearer(ctx, c), desc, cc, method, opts...)
		if err != nil {
			return nil, Explain(err)
		}
		return explainingStream{stream}, nil
	}
}

type explainingStream struct {
	grpc.ClientStream
}

func (s explainingStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if errors.Is(err, io.EOF) {
		return err
	}
	return Explain(err)
}

func (s explainingStream) SendMsg(m any) error {
	err := s.ClientStream.SendMsg(m)
	if errors.Is(err, io.EOF) {
		return err
	}
	return Explain(err)
}

// Explain rewrites an UNAUTHENTICATED or PERMISSION_DENIED status into one whose message says what
// to do; the code is unchanged (status.Code(err) still works) and the platform's own reason is
// kept. Any other error is returned as is.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	if strings.HasPrefix(st.Message(), "Zequent refused the call") {
		return err
	}
	var advice string
	switch st.Code() {
	case codes.Unauthenticated:
		advice = "no client credential was sent, or it is expired, revoked or not issued by this installation. " +
			"Set " + EnvVar + " or pass auth.DialOptions(token); an organization administrator issues one in the " +
			"console (Access & Integrations > Credentials, kind 'client')"
	case codes.PermissionDenied:
		advice = "this client credential may not do this - it reaches only its own organization's assets, " +
			"Applications and runs, and never administration"
	default:
		return err
	}
	cause := ""
	if st.Message() != "" {
		cause = fmt.Sprintf(" (%s)", st.Message())
	}
	return status.Error(st.Code(), "Zequent refused the call: "+advice+cause)
}
