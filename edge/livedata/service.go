// Package livedata provides the LiveDataService interface and its gRPC-backed
// implementation for producing telemetry data over persistent bidirectional streams.
package livedata

import (
	"context"

	"edge-go-sdk/adapter/domains"
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"
)

// LiveDataService manages persistent gRPC client-streaming connections to the
// live-data backend and routes telemetry frames through them.
type LiveDataService interface {
	// ProduceTelemetryData maps a domain TelemetryRequestData to the proto format
	// and forwards it over the persistent stream for the device's SN.
	ProduceTelemetryData(ctx context.Context, data *domains.TelemetryRequestData) error

	// ProduceTelemetry sends a pre-built proto request directly.
	// Use this for advanced cases where you need full control over the proto payload.
	ProduceTelemetry(ctx context.Context, deviceSN string, req *zqntpb.ProduceTelemetryRequest) error

	// CloseStream closes the persistent stream for the given device.
	CloseStream(ctx context.Context, deviceSN string) error

	// CloseAllStreams closes all active streams. Call this during shutdown.
	CloseAllStreams(ctx context.Context) error
}
