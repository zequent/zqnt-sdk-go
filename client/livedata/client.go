// Package livedata is a client for LiveDataService — telemetry/detection/notification streaming
// and live-stream/camera control. Unlike client-java-sdk's LiveData, this package does not manage
// reconnection for you: StreamTelemetry/StreamDetections/StreamNotifications hand back the raw
// gRPC server-streaming client, and it's the caller's job to Recv() in a loop and redial on error.
package livedata

import (
	"context"
	"fmt"
	"time"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	detectionpb "github.com/Zequent/zqnt-client-sdk-go/gen/common/detection/proto"
	devicecontrol "github.com/Zequent/zqnt-client-sdk-go/gen/devicecontrol/contracts/proto"
	eventspb "github.com/Zequent/zqnt-client-sdk-go/gen/events/proto"
	livedatapb "github.com/Zequent/zqnt-client-sdk-go/gen/livedata/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client wraps LiveDataServiceClient.
type Client struct {
	grpc livedatapb.LiveDataServiceClient
}

// New wraps an existing gRPC connection — dial it yourself with your own retry/TLS policy.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: livedatapb.NewLiveDataServiceClient(conn)}
}

// StreamTelemetry opens a server-streaming telemetry subscription for the asset identified in
// req.Base.Sn. frequencyMs/durationSeconds are optional (pass 0 to omit either).
func (c *Client) StreamTelemetry(ctx context.Context, sn string, frequencyMs, durationSeconds int32) (grpc.ServerStreamingClient[livedatapb.LiveDataTelemetryResponse], error) {
	req := &livedatapb.StreamTelemetryRequest{Base: requestBase(sn), Command: devicecontrol.LiveDataServiceCommand_LIVE_DATA_COMMAND_START_TELEMETRY_STREAM}
	if frequencyMs != 0 {
		req.FrequencyMs = &frequencyMs
	}
	if durationSeconds != 0 {
		req.Duration = &durationSeconds
	}
	stream, err := c.grpc.StreamTelemetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("livedata: StreamTelemetry(%s): %w", sn, err)
	}
	return stream, nil
}

// StreamDetections opens a server-streaming detection subscription for sn.
func (c *Client) StreamDetections(ctx context.Context, sn string) (grpc.ServerStreamingClient[livedatapb.LiveDataDetectionResponse], error) {
	stream, err := c.grpc.StreamDetections(ctx, &detectionpb.DetectionStreamRequest{Base: requestBase(sn)})
	if err != nil {
		return nil, fmt.Errorf("livedata: StreamDetections(%s): %w", sn, err)
	}
	return stream, nil
}

// StreamNotifications opens a server-streaming notification subscription for sn, optionally
// filtered to eventTypes (pass nil for every type).
func (c *Client) StreamNotifications(ctx context.Context, sn string, eventTypes []eventspb.NotificationEventType) (grpc.ServerStreamingClient[eventspb.NotificationResponse], error) {
	stream, err := c.grpc.StreamNotifications(ctx, &eventspb.StreamNotificationsRequest{Base: requestBase(sn), EventTypes: eventTypes})
	if err != nil {
		return nil, fmt.Errorf("livedata: StreamNotifications(%s): %w", sn, err)
	}
	return stream, nil
}

// errorResponse is satisfied by every CommandResponse-shaped RPC response in this package.
type errorResponse interface {
	GetHasErrors() bool
	GetError() *base.GlobalErrorMessage
}

func unwrap[Resp errorResponse](name string, resp Resp, err error) (Resp, error) {
	var zero Resp
	if err != nil {
		return zero, fmt.Errorf("livedata: %s: %w", name, err)
	}
	if resp.GetHasErrors() {
		return zero, fmt.Errorf("livedata: %s: %s", name, resp.GetError().GetErrorMessage())
	}
	return resp, nil
}

func (c *Client) StartLiveStream(ctx context.Context, req *devicecontrol.LiveStreamStartCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.StartLiveStream(ctx, req)
	return unwrap("StartLiveStream", resp, err)
}

func (c *Client) StopLiveStream(ctx context.Context, req *devicecontrol.LiveStreamStopCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.StopLiveStream(ctx, req)
	return unwrap("StopLiveStream", resp, err)
}

func (c *Client) ChangeLens(ctx context.Context, req *devicecontrol.ChangeCameraLensCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.ChangeLens(ctx, req)
	return unwrap("ChangeLens", resp, err)
}

func (c *Client) ChangeZoom(ctx context.Context, req *devicecontrol.ChangeCameraZoomCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.ChangeZoom(ctx, req)
	return unwrap("ChangeZoom", resp, err)
}

func (c *Client) StartRecording(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.StartRecording(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("StartRecording", resp, err)
}

func (c *Client) StopRecording(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.StopRecording(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("StopRecording", resp, err)
}

func (c *Client) CapturePhoto(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.CapturePhoto(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("CapturePhoto", resp, err)
}

func requestBase(sn string) *base.RequestBase {
	return &base.RequestBase{Tid: newTid(), Sn: sn, Timestamp: timestamppb.Now()}
}

func newTid() string {
	return fmt.Sprintf("client-go-sdk-ld-%d", time.Now().UnixNano())
}
