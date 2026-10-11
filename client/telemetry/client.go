// Package telemetry watches live telemetry on a Zequent 3.x platform. It is the client of
// zqnt.telemetry.v3.TelemetryService, served by live-data.
//
// A TelemetrySample has the fields every asset shares (position, relative altitude, heading,
// speeds, battery); everything device-specific is in its Details, under the keys the asset declares
// in its capabilities' telemetry_fields.
//
//	conn, err := cfg.Dial(cfg.LiveData)
//	watch, err := telemetry.New(conn).WatchTelemetry(ctx, []string{"DOCK-1"}, telemetry.WithMinInterval(time.Second))
//	defer watch.Close()
//	for {
//		item, err := watch.Recv()
//		...
//	}
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	commonv3 "github.com/zequent/zqnt-api-go/v3/gen/zqnt/common/v3"
	telemetryv3 "github.com/zequent/zqnt-api-go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
)

// DefaultHeartbeatTimeout allows roughly three missed 10-second platform heartbeats.
const DefaultHeartbeatTimeout = 35 * time.Second

// ErrSilent is returned by Recv when neither a sample nor a heartbeat arrived within the heartbeat
// timeout. The stream is gone; watch again.
var ErrSilent = errors.New("telemetry: no sample and no heartbeat within the heartbeat timeout")

// Client wraps the generated TelemetryServiceClient.
type Client struct {
	grpc telemetryv3.TelemetryServiceClient
}

// New wraps a connection to live-data, e.g. cfg.Dial(cfg.LiveData).
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: telemetryv3.NewTelemetryServiceClient(conn)}
}

// Option adjusts a watch.
type Option func(*options)

type options struct {
	minInterval      time.Duration
	heartbeatTimeout time.Duration
}

// WithMinInterval keeps at most one sample per asset per interval; without it every sample comes.
func WithMinInterval(d time.Duration) Option {
	return func(o *options) { o.minInterval = d }
}

// WithHeartbeatTimeout replaces DefaultHeartbeatTimeout.
func WithHeartbeatTimeout(d time.Duration) Option {
	return func(o *options) { o.heartbeatTimeout = d }
}

// WatchTelemetry streams the samples of the given assets from now on, until ctx ends or Close. No
// asset watches every asset of the caller's organization; a dock's serial also brings the samples
// of its drone.
func (c *Client) WatchTelemetry(ctx context.Context, sns []string, opts ...Option) (*Watch, error) {
	o := options{heartbeatTimeout: DefaultHeartbeatTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	req := &telemetryv3.WatchTelemetryRequest{Context: &commonv3.RequestContext{RequestId: newRequestID()}}
	for _, sn := range sns {
		if sn == "" {
			return nil, errors.New("telemetry: WatchTelemetry: an empty serial")
		}
		req.Assets = append(req.Assets, &commonv3.AssetRef{Sn: sn})
	}
	if o.minInterval > 0 {
		req.MinInterval = durationpb.New(o.minInterval)
	}
	ctx, cancel := context.WithCancel(ctx)
	stream, err := c.grpc.WatchTelemetry(ctx, req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("telemetry: WatchTelemetry: %w", err)
	}
	return &Watch{stream: stream, cancel: cancel, timeout: o.heartbeatTimeout}, nil
}

// Watch is a running telemetry watch. It does not reconnect.
type Watch struct {
	stream  grpc.ServerStreamingClient[telemetryv3.WatchTelemetryResponse]
	cancel  context.CancelFunc
	timeout time.Duration
	silent  atomic.Bool
}

// Recv returns the next item: GetSample() or GetSourceStatus() is set, heartbeats are skipped.
// Every watched asset's SourceStatus comes first, then one each time an asset goes online, stale
// or silent. Recv returns io.EOF when the platform ended the watch, ErrSilent when the stream went
// quiet, or the gRPC error that ended it.
func (w *Watch) Recv() (*telemetryv3.WatchTelemetryResponse, error) {
	for {
		timer := time.AfterFunc(w.timeout, func() {
			w.silent.Store(true)
			w.cancel()
		})
		resp, err := w.stream.Recv()
		timer.Stop()
		if err != nil {
			w.cancel()
			if w.silent.Load() {
				return nil, ErrSilent
			}
			return nil, err
		}
		if resp.GetHeartbeat() == nil {
			return resp, nil
		}
	}
}

// Close ends the watch.
func (w *Watch) Close() {
	w.cancel()
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "client-go-sdk-" + hex.EncodeToString(b)
}
