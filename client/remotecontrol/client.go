// Package remotecontrol is a client for RemoteControlService — the client-facing command gateway
// for manual flight control, dock operations, and asset/payload runtime discovery. It shares its
// command contracts with EdgeAdapterService (device-control-contracts.proto), so the same request
// and response shapes flow end to end from a customer app through to the edge adapter.
package remotecontrol

import (
	"context"
	"fmt"
	"time"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	devicecontrol "github.com/Zequent/zqnt-client-sdk-go/gen/devicecontrol/contracts/proto"
	remotecontrolpb "github.com/Zequent/zqnt-client-sdk-go/gen/remotecontrol/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client wraps RemoteControlServiceClient, converting the wire-level has_errors/error convention
// into an idiomatic Go error return.
type Client struct {
	grpc remotecontrolpb.RemoteControlServiceClient
}

// New wraps an existing gRPC connection — dial it yourself with your own retry/TLS policy.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: remotecontrolpb.NewRemoteControlServiceClient(conn)}
}

// errorResponse is satisfied by every CommandResponse-shaped RPC response in this package.
type errorResponse interface {
	GetHasErrors() bool
	GetError() *base.GlobalErrorMessage
}

// unwrap takes the (resp, err) pair a generated gRPC client method returns and converts the
// wire-level has_errors/error convention into an idiomatic Go error.
func unwrap[Resp errorResponse](name string, resp Resp, err error) (Resp, error) {
	var zero Resp
	if err != nil {
		return zero, fmt.Errorf("remotecontrol: %s: %w", name, err)
	}
	if resp.GetHasErrors() {
		return zero, fmt.Errorf("remotecontrol: %s: %s", name, resp.GetError().GetErrorMessage())
	}
	return resp, nil
}

// ---- Capability / runtime discovery -----------------------------------------

// ReportAssetRuntime is normally called by an edge adapter, not a customer app — exposed here for
// completeness/testing.
func (c *Client) ReportAssetRuntime(ctx context.Context, req *devicecontrol.ReportAssetRuntimeRequest) (*devicecontrol.ReportAssetRuntimeResponse, error) {
	resp, err := c.grpc.ReportAssetRuntime(ctx, req)
	return unwrap("ReportAssetRuntime", resp, err)
}

// GetAssetRuntime returns the latest payload/capability snapshot Remote Control has cached for an asset.
func (c *Client) GetAssetRuntime(ctx context.Context, assetSn, assetID string) (*devicecontrol.AssetRuntimeSnapshot, error) {
	req := &devicecontrol.GetAssetRuntimeRequest{Base: requestBase(assetSn), AssetSn: assetSn}
	if assetID != "" {
		req.AssetId = &assetID
	}
	grpcResp, err := c.grpc.GetAssetRuntime(ctx, req)
	resp, err := unwrap("GetAssetRuntime", grpcResp, err)
	if err != nil {
		return nil, err
	}
	return resp.GetRuntime(), nil
}

// GetCapabilities returns the current capability snapshot for sn.
func (c *Client) GetCapabilities(ctx context.Context, sn string) (*devicecontrol.AssetCapabilities, error) {
	resp, err := c.grpc.GetCapabilities(ctx, &devicecontrol.AssetCapabilitiesRequest{Base: requestBase(sn)})
	if err != nil {
		return nil, fmt.Errorf("remotecontrol: GetCapabilities(%s): %w", sn, err)
	}
	if resp.GetError() != nil {
		return nil, fmt.Errorf("remotecontrol: GetCapabilities(%s): %s", sn, resp.GetError().GetErrorMessage())
	}
	return resp.GetCapabilities(), nil
}

// ---- Flight control -----------------------------------------------------------

func (c *Client) TakeOff(ctx context.Context, sn string, coordinate *devicecontrol.GeoCoordinate) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.TakeOff(ctx, &devicecontrol.CoordinateCommandRequest{Base: requestBase(sn), Coordinate: coordinate})
	return unwrap("TakeOff", resp, err)
}

func (c *Client) GoTo(ctx context.Context, sn string, coordinate *devicecontrol.GeoCoordinate) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.GoTo(ctx, &devicecontrol.CoordinateCommandRequest{Base: requestBase(sn), Coordinate: coordinate})
	return unwrap("GoTo", resp, err)
}

// ReturnToHome commands the asset home. altitude is optional (pass 0 to omit — the device's
// default RTH altitude is used).
func (c *Client) ReturnToHome(ctx context.Context, sn string, altitude float32) (*devicecontrol.CommandResponse, error) {
	req := &devicecontrol.ReturnToHomeRequest{}
	if altitude != 0 {
		req.Altitude = &altitude
	}
	resp, err := c.grpc.ReturnToHome(ctx, &devicecontrol.ReturnToHomeCommandRequest{Base: requestBase(sn), Request: req})
	return unwrap("ReturnToHome", resp, err)
}

// ---- Manual control -------------------------------------------------------------

// clientID/userID identify who is taking control; sessionID scopes the manual-control session.
func (c *Client) EnterManualControl(ctx context.Context, sn, clientID, userID, sessionID string) (*devicecontrol.CommandResponse, error) {
	req := &devicecontrol.ManualControlRequest{ClientId: clientID, UserId: userID, SessionId: sessionID}
	resp, err := c.grpc.EnterManualControl(ctx, &devicecontrol.ManualControlCommandRequest{Base: requestBase(sn), Request: req})
	return unwrap("EnterManualControl", resp, err)
}

func (c *Client) ExitManualControl(ctx context.Context, sn, clientID, userID, sessionID string) (*devicecontrol.CommandResponse, error) {
	req := &devicecontrol.ManualControlRequest{ClientId: clientID, UserId: userID, SessionId: sessionID}
	resp, err := c.grpc.ExitManualControl(ctx, &devicecontrol.ManualControlCommandRequest{Base: requestBase(sn), Request: req})
	return unwrap("ExitManualControl", resp, err)
}

// OpenManualControlInputStream opens the client-streaming RC-input channel — send one
// ManualControlInputCommandRequest per tick, then CloseAndRecv when done.
func (c *Client) OpenManualControlInputStream(ctx context.Context) (grpc.ClientStreamingClient[devicecontrol.ManualControlInputCommandRequest, devicecontrol.CommandResponse], error) {
	stream, err := c.grpc.ManualControlInput(ctx)
	if err != nil {
		return nil, fmt.Errorf("remotecontrol: OpenManualControlInputStream: %w", err)
	}
	return stream, nil
}

func (c *Client) LookAt(ctx context.Context, sn string, coordinate *devicecontrol.GeoCoordinate, locked bool) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.LookAt(ctx, &devicecontrol.LookAtCommandRequest{Base: requestBase(sn), Coordinate: coordinate, Locked: &locked})
	return unwrap("LookAt", resp, err)
}

func (c *Client) CapturePhoto(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.CapturePhoto(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("CapturePhoto", resp, err)
}

func (c *Client) PlayTTSAudio(ctx context.Context, req *devicecontrol.TextToSpeechCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.PlayTTSAudio(ctx, req)
	return unwrap("PlayTTSAudio", resp, err)
}

func (c *Client) LiveStreamSplitScreen(ctx context.Context, sn string, enabled bool) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.LiveStreamSplitScreen(ctx, &devicecontrol.ToggleCommandRequest{Base: requestBase(sn), Enabled: enabled})
	return unwrap("LiveStreamSplitScreen", resp, err)
}

// ---- Detection --------------------------------------------------------------------

func (c *Client) ControlDetection(ctx context.Context, req *devicecontrol.DetectionControlCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.ControlDetection(ctx, req)
	return unwrap("ControlDetection", resp, err)
}

// ---- Dock commands ----------------------------------------------------------------

func (c *Client) OpenCover(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.OpenCover(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("OpenCover", resp, err)
}

func (c *Client) CloseCover(ctx context.Context, sn string, force bool) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.CloseCover(ctx, &devicecontrol.CloseCoverCommandRequest{Base: requestBase(sn), Force: &force})
	return unwrap("CloseCover", resp, err)
}

func (c *Client) StartCharging(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.StartCharging(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("StartCharging", resp, err)
}

func (c *Client) StopCharging(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.StopCharging(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("StopCharging", resp, err)
}

// ---- Asset management ---------------------------------------------------------------

func (c *Client) RebootAsset(ctx context.Context, sn string) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.RebootAsset(ctx, &devicecontrol.EmptyCommandRequest{Base: requestBase(sn)})
	return unwrap("RebootAsset", resp, err)
}

func (c *Client) BootSubAsset(ctx context.Context, sn string, enabled bool) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.BootSubAsset(ctx, &devicecontrol.ToggleCommandRequest{Base: requestBase(sn), Enabled: enabled})
	return unwrap("BootSubAsset", resp, err)
}

// ---- Debug & maintenance -------------------------------------------------------------

func (c *Client) SetRemoteDebugMode(ctx context.Context, sn string, enabled bool) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.SetRemoteDebugMode(ctx, &devicecontrol.ToggleCommandRequest{Base: requestBase(sn), Enabled: enabled})
	return unwrap("SetRemoteDebugMode", resp, err)
}

func (c *Client) ChangeAcMode(ctx context.Context, req *devicecontrol.ChangeAcModeCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.ChangeAcMode(ctx, req)
	return unwrap("ChangeAcMode", resp, err)
}

// ---- Camera ---------------------------------------------------------------------------

func (c *Client) ChangeLens(ctx context.Context, req *devicecontrol.ChangeCameraLensCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.ChangeLens(ctx, req)
	return unwrap("ChangeLens", resp, err)
}

func (c *Client) ChangeZoom(ctx context.Context, req *devicecontrol.ChangeCameraZoomCommandRequest) (*devicecontrol.CommandResponse, error) {
	resp, err := c.grpc.ChangeZoom(ctx, req)
	return unwrap("ChangeZoom", resp, err)
}

// ---- Custom / integrator-defined commands ----------------------------------------------

func (c *Client) SendCustomCommand(ctx context.Context, sn, commandID string, params *structpb.Struct, target *devicecontrol.CapabilityTarget) (*devicecontrol.CustomCommandResponse, error) {
	req := &devicecontrol.CustomCommandRequest{Base: requestBase(sn), CommandId: commandID, Params: params, Target: target}
	resp, err := c.grpc.SendCustomCommand(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("remotecontrol: SendCustomCommand(%s): %w", commandID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("remotecontrol: SendCustomCommand(%s): %s", commandID, resp.GetError().GetErrorMessage())
	}
	return resp, nil
}

func requestBase(sn string) *base.RequestBase {
	return &base.RequestBase{Tid: newTid(), Sn: sn, Timestamp: timestamppb.Now()}
}

func newTid() string {
	return fmt.Sprintf("client-go-sdk-rc-%d", time.Now().UnixNano())
}
