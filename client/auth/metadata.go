package auth

import (
	"context"

	"google.golang.org/grpc/metadata"
)

// withAuthorization replaces any authorization header already on the outgoing context.
func withAuthorization(ctx context.Context, value string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("authorization", value)
	return metadata.NewOutgoingContext(ctx, md)
}

// hasAuthorization reports whether the outgoing context already carries an authorization header.
func hasAuthorization(ctx context.Context) bool {
	md, ok := metadata.FromOutgoingContext(ctx)
	return ok && len(md.Get("authorization")) > 0
}
