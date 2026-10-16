// Package streams lists the live video streams on a Zequent 3.x platform. It is the client of
// zqnt.stream.v3.LiveStreamService, served by live-data. A stream is listed while something
// publishes to it on the media server, whatever started it.
//
//	conn, err := cfg.Dial(cfg.LiveData)
//	live, err := streams.New(conn).ListLiveStreams(ctx)
package streams

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

// ListLiveStreams returns every stream of the caller's organization that is live right now, with
// its asset, media server path, start time and number of viewers. The error carries the gRPC
// status (status.Code(err)): codes.Unavailable when the platform has no current answer from the
// media server, which is worth asking again.
func (c *Client) ListLiveStreams(ctx context.Context) ([]*streamv3.LiveStream, error) {
	resp, err := c.grpc.ListLiveStreams(ctx, &streamv3.ListLiveStreamsRequest{
		Context: &commonv3.RequestContext{RequestId: newRequestID()},
	})
	if err != nil {
		return nil, fmt.Errorf("streams: ListLiveStreams: %w", err)
	}
	return resp.GetStreams(), nil
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "client-go-sdk-" + hex.EncodeToString(b)
}
