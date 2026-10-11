// Package telemetry watches live telemetry, detections and alerts on a Zequent 3.x platform. It is
// the client of zqnt.telemetry.v3.TelemetryService, served by live-data.
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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DefaultHeartbeatTimeout allows roughly three missed 10-second platform heartbeats.
const DefaultHeartbeatTimeout = 35 * time.Second

// ErrSilent is returned by Recv when neither a sample nor a heartbeat arrived within the heartbeat
// timeout. The stream is gone; watch again.
var ErrSilent = errors.New("telemetry: no item and no heartbeat within the heartbeat timeout")

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
	minSeverity      telemetryv3.AlertSeverity
	heartbeatTimeout time.Duration
}

// WithMinInterval keeps at most one sample per asset per interval; without it every sample comes.
// Only WatchTelemetry uses it.
func WithMinInterval(d time.Duration) Option {
	return func(o *options) { o.minInterval = d }
}

// WithMinSeverity drops alerts below the given severity; without it every alert comes. Only
// WatchAlerts uses it.
func WithMinSeverity(severity telemetryv3.AlertSeverity) Option {
	return func(o *options) { o.minSeverity = severity }
}

// WithHeartbeatTimeout replaces DefaultHeartbeatTimeout.
func WithHeartbeatTimeout(d time.Duration) Option {
	return func(o *options) { o.heartbeatTimeout = d }
}

// Item is a response of a watch: an item, or a heartbeat that Recv skips.
type Item interface {
	GetHeartbeat() *timestamppb.Timestamp
}

// WatchTelemetry streams the samples of the given assets from now on, until ctx ends or Close. No
// asset watches every asset of the caller's organization; a dock's serial also brings the samples
// of its drone. Recv returns items with GetSample() or GetSourceStatus() set: every watched asset's
// SourceStatus comes first, then one each time an asset goes online, stale or silent.
func (c *Client) WatchTelemetry(ctx context.Context, sns []string, opts ...Option) (*Watch[*telemetryv3.WatchTelemetryResponse], error) {
	o := collect(opts)
	assets, err := assetRefs("WatchTelemetry", sns)
	if err != nil {
		return nil, err
	}
	req := &telemetryv3.WatchTelemetryRequest{Context: requestContext(), Assets: assets}
	if o.minInterval > 0 {
		req.MinInterval = durationpb.New(o.minInterval)
	}
	return watch(ctx, "WatchTelemetry", o, func(ctx context.Context) (func() (*telemetryv3.WatchTelemetryResponse, error), error) {
		stream, err := c.grpc.WatchTelemetry(ctx, req)
		if err != nil {
			return nil, err
		}
		return stream.Recv, nil
	})
}

// WatchDetections streams the detection batches of the given assets from now on (GetBatch()), until
// ctx ends or Close. No asset watches every asset of the caller's organization.
func (c *Client) WatchDetections(ctx context.Context, sns []string, opts ...Option) (*Watch[*telemetryv3.WatchDetectionsResponse], error) {
	o := collect(opts)
	assets, err := assetRefs("WatchDetections", sns)
	if err != nil {
		return nil, err
	}
	req := &telemetryv3.WatchDetectionsRequest{Context: requestContext(), Assets: assets}
	return watch(ctx, "WatchDetections", o, func(ctx context.Context) (func() (*telemetryv3.WatchDetectionsResponse, error), error) {
		stream, err := c.grpc.WatchDetections(ctx, req)
		if err != nil {
			return nil, err
		}
		return stream.Recv, nil
	})
}

// WatchAlerts streams the alerts of the given assets from now on (GetAlert()), until ctx ends or
// Close. No asset watches every asset of the caller's organization.
func (c *Client) WatchAlerts(ctx context.Context, sns []string, opts ...Option) (*Watch[*telemetryv3.WatchAlertsResponse], error) {
	o := collect(opts)
	assets, err := assetRefs("WatchAlerts", sns)
	if err != nil {
		return nil, err
	}
	req := &telemetryv3.WatchAlertsRequest{Context: requestContext(), Assets: assets, MinSeverity: o.minSeverity}
	return watch(ctx, "WatchAlerts", o, func(ctx context.Context) (func() (*telemetryv3.WatchAlertsResponse, error), error) {
		stream, err := c.grpc.WatchAlerts(ctx, req)
		if err != nil {
			return nil, err
		}
		return stream.Recv, nil
	})
}

func collect(opts []Option) options {
	o := options{heartbeatTimeout: DefaultHeartbeatTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func assetRefs(method string, sns []string) ([]*commonv3.AssetRef, error) {
	assets := make([]*commonv3.AssetRef, 0, len(sns))
	for _, sn := range sns {
		if sn == "" {
			return nil, fmt.Errorf("telemetry: %s: an empty serial", method)
		}
		assets = append(assets, &commonv3.AssetRef{Sn: sn})
	}
	return assets, nil
}

func requestContext() *commonv3.RequestContext {
	return &commonv3.RequestContext{RequestId: newRequestID()}
}

func watch[T Item](ctx context.Context, method string, o options,
	open func(context.Context) (func() (T, error), error)) (*Watch[T], error) {
	ctx, cancel := context.WithCancel(ctx)
	recv, err := open(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("telemetry: %s: %w", method, err)
	}
	return &Watch[T]{recv: recv, cancel: cancel, timeout: o.heartbeatTimeout}, nil
}

// Watch is a running watch. It does not reconnect.
type Watch[T Item] struct {
	recv    func() (T, error)
	cancel  context.CancelFunc
	timeout time.Duration
	silent  atomic.Bool
}

// Recv returns the next item; heartbeats are skipped. It returns io.EOF when the platform ended the
// watch, ErrSilent when the stream went quiet, or the gRPC error that ended it.
func (w *Watch[T]) Recv() (T, error) {
	for {
		timer := time.AfterFunc(w.timeout, func() {
			w.silent.Store(true)
			w.cancel()
		})
		resp, err := w.recv()
		timer.Stop()
		if err != nil {
			w.cancel()
			var zero T
			if w.silent.Load() {
				return zero, ErrSilent
			}
			return zero, err
		}
		if resp.GetHeartbeat() == nil {
			return resp, nil
		}
	}
}

// Close ends the watch.
func (w *Watch[T]) Close() {
	w.cancel()
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "client-go-sdk-" + hex.EncodeToString(b)
}
