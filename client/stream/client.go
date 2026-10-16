// Package stream lists the live video streams of a Zequent 3.x platform. It is the client of
// zqnt.stream.v3.LiveStreamService, served by live-data.
//
//	conn, err := cfg.Dial(cfg.LiveData)
//	streams, err := stream.New(conn).ListLiveStreams(ctx)
//	for _, s := range streams {
//		fmt.Println(s.GetAsset().GetSn(), s.GetPath(), s.GetSince().AsTime(), s.GetViewers())
//	}
package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	streamv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/stream/v3"
	"google.golang.org/grpc"
)

// Client wraps the generated LiveStreamServiceClient.
type Client struct {
	grpc streamv3.LiveStreamServiceClient
}

// New wraps a connection to live-data, e.g. cfg.Dial(cfg.LiveData).
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: streamv3.NewLiveStreamServiceClient(conn)}
}

// ListLiveStreams returns every stream of the caller's organization that is live right now: the
// asset publishing it, the media server's path, since when and how many sessions read it. The
// error keeps its gRPC status: PermissionDenied when the organization's license has no live
// streams, Unavailable when the platform has no current answer from the media server.
func (c *Client) ListLiveStreams(ctx context.Context) ([]*streamv3.LiveStream, error) {
	resp, err := c.grpc.ListLiveStreams(ctx, &streamv3.ListLiveStreamsRequest{
		Context: &commonv3.RequestContext{RequestId: newRequestID()},
	})
	if err != nil {
		return nil, fmt.Errorf("stream: ListLiveStreams: %w", err)
	}
	return resp.GetStreams(), nil
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "client-go-sdk-" + hex.EncodeToString(b)
}
