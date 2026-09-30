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
