// Package livedata provides the LiveDataService interface and its gRPC-backed implementation for
// producing telemetry and notifications over persistent client-streaming connections.
//
// Telemetry is the continuous flow; notifications are the occasional ones, of which
// command-execution events are the kind an adapter running capability executions cannot skip --
// see PublishCommandExecutionEvent.
package livedata

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	livedatapb "github.com/zequent/zqnt-utils-golang/v2/gen/livedata/proto"
)

// LiveDataService manages persistent gRPC client-streaming connections to the
// live-data backend and routes telemetry frames through them.
type LiveDataService interface {
	// ProduceTelemetryData maps a domain TelemetryRequestData to the proto format
	// and forwards it over the persistent stream for the device's SN.
	ProduceTelemetryData(ctx context.Context, data *domains.TelemetryRequestData) error

	// ProduceTelemetry sends a pre-built proto request directly.
	// Use this for advanced cases where you need full control over the proto payload.
	ProduceTelemetry(ctx context.Context, deviceSN string, req *livedatapb.ProduceTelemetryRequest) error

	// CloseStream closes the persistent stream for the given device.
	CloseStream(ctx context.Context, deviceSN string) error

	// CloseAllStreams closes all active telemetry streams. Call this during shutdown.
	CloseAllStreams(ctx context.Context) error

	// PublishCommandExecutionEvent reports one stage of a physical command's lifecycle -- accepted,
	// running with progress, succeeded, failed or cancelled -- to the platform.
	//
	// This is not optional bookkeeping for an adapter that runs capability executions.
	// mission-autonomy dispatches the typed flight commands and every custom command
	// asynchronously: the skill node goes to ACCEPTED and waits for an event under the execution id
	// it sent, so a command whose RPC returned success but which reports no event leaves its node
	// running until the whole execution times out.
	PublishCommandExecutionEvent(ctx context.Context, event *domains.CommandExecutionEvent) error

	// CloseNotificationStream closes the notification stream. Call this during shutdown.
	CloseNotificationStream(ctx context.Context) error
}
