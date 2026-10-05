package adaptergrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NotSupportedCode is the error code of a v3 command the adapter cannot place.
const NotSupportedCode = "command.not_supported"

// ServerV3 implements the v3 edge contract (zqnt.edge.v3.EdgeAdapterService) next to the v2
// Server, on the same EdgeAdapter.
//
// v3 has no per-command RPCs: every command is an ExecuteCommand with a dotted id. A built-in id
// (flight.takeoff, dock.open_cover, ...) runs the typed EdgeAdapter method that implements it --
// the method every adapter written for v2 already has -- and anything else goes to
// SendCustomCommand. An existing adapter therefore answers v3 without a change; it only has to
// register this server too (RegisterWith).
type ServerV3 struct {
	edgev3.UnimplementedEdgeAdapterServiceServer
	adapter adapter.EdgeAdapter
	log     *slog.Logger
}

// NewServerV3 creates the v3 server for the given EdgeAdapter.
func NewServerV3(a adapter.EdgeAdapter, log *slog.Logger) *ServerV3 {
	return &ServerV3{adapter: a, log: log}
}

// RegisterWith registers the v3 service with the given gRPC server, alongside the v2 one.
func (s *ServerV3) RegisterWith(gs *grpc.Server) {
	edgev3.RegisterEdgeAdapterServiceServer(gs, s)
}

// GetCapabilities returns the adapter's capability snapshot in the v3 shape.
func (s *ServerV3) GetCapabilities(ctx context.Context, req *edgev3.GetCapabilitiesRequest) (*edgev3.GetCapabilitiesResponse, error) {
	caps, err := s.adapter.GetCapabilities(ctx, req.GetAsset().GetSn())
	if err != nil {
		return nil, err
	}
	set := &capabilityv3.CapabilitySet{
		AssetSn:       caps.SN,
		AssetType:     caps.AssetType,
		ObservedAt:    timestamppb.New(caps.Timestamp),
		SnapshotState: capabilityv3.SnapshotState_SNAPSHOT_STATE_CURRENT,
	}
	if set.AssetSn == "" {
		set.AssetSn = req.GetAsset().GetSn()
	}
	for _, c := range caps.Capabilities {
		set.Capabilities = append(set.Capabilities, s.capability(c))
	}
	return &edgev3.GetCapabilitiesResponse{Capabilities: set}, nil
}

func (s *ServerV3) capability(c domains.Capability) *capabilityv3.Capability {
	state := capabilityv3.CapabilityState_CAPABILITY_STATE_UNSUPPORTED
	if c.Available {
		state = capabilityv3.CapabilityState_CAPABILITY_STATE_AVAILABLE
	}
	out := &capabilityv3.Capability{
		CommandId:     c.Command,
		DisplayName:   c.Command,
		Description:   c.Description,
		State:         state,
		Metadata:      c.Metadata,
		SchemaVersion: c.SchemaVersion,
		SkillId:       c.SkillID,
		Provider:      c.Provider,
		// v2 and v3 number these enums identically.
		Source: capabilityv3.CapabilitySource(c.Source),
		Target: &capabilityv3.Target{Type: capabilityv3.TargetType(c.TargetType)},
	}
	if c.UnavailableReason != nil {
		out.UnavailableReason = *c.UnavailableReason
	}
	if c.TargetRef != nil {
		out.Target.Ref = *c.TargetRef
	}
	out.InputSchema = s.schema(c.Command, "input_schema", c.InputSchema)
	out.OutputSchema = s.schema(c.Command, "output_schema", c.OutputSchema)
	for _, e := range c.Errors {
		out.Errors = append(out.Errors, &capabilityv3.CapabilityErrorSpec{Code: e.Code, Description: e.Description})
	}
	return out
}

func (s *ServerV3) schema(command, field string, m map[string]any) *structpb.Struct {
	if len(m) == 0 {
		return nil
	}
	st, err := structpb.NewStruct(m)
	if err != nil {
		s.log.Warn("capability schema dropped: not representable on the wire", "command", command, "field", field, "error", err)
		return nil
	}
	return st
}

// ExecuteCommand runs one command by its dotted id.
func (s *ServerV3) ExecuteCommand(ctx context.Context, req *edgev3.ExecuteCommandRequest) (*edgev3.ExecuteCommandResponse, error) {
	cmd := req.GetCommand()
	sn := cmd.GetAsset().GetSn()
	tid := req.GetContext().GetRequestId()
	params := cmd.GetParams().AsMap()

	result, err := s.dispatch(ctx, sn, tid, cmd, params)
	if err != nil {
		s.log.Error("v3 ExecuteCommand failed", "command", cmd.GetCommandId(), "sn", sn, "error", err)
		result = domains.Error(err.Error(), sn)
	}
	return &edgev3.ExecuteCommandResponse{Result: commandResult(cmd.GetCommandId(), req.GetCommandExecutionId(), result)}, nil
}

func (s *ServerV3) dispatch(ctx context.Context, sn, tid string, cmd *capabilityv3.Command, params map[string]any) (*domains.CommandResult, error) {
	if run, ok := builtIn(s.adapter, sn, tid, cmd.GetCommandId(), params); ok {
		return run(ctx)
	}
	custom := &domains.CustomCommandRequest{SN: sn, TID: tid, CommandID: cmd.GetCommandId(), Params: params}
	if ref := cmd.GetTarget().GetRef(); ref != "" {
		custom.TargetRef = &ref
	}
	return s.adapter.SendCustomCommand(ctx, custom)
}

// CancelCommand stops a running command the way v2 does, through StopTask with the id the command
// was accepted under.
func (s *ServerV3) CancelCommand(ctx context.Context, req *edgev3.CancelCommandRequest) (*edgev3.CancelCommandResponse, error) {
	result, err := s.adapter.StopTask(ctx, "", req.GetCommandExecutionId(), req.GetContext().GetRequestId())
	if err != nil {
		result = domains.Error(err.Error(), "")
	}
	out := commandResult("", req.GetCommandExecutionId(), result)
	if out.State == capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED {
		out.State = capabilityv3.CommandState_COMMAND_STATE_CANCELLED
	}
	return &edgev3.CancelCommandResponse{Result: out}, nil
}

// StreamManualControl feeds stick input to the adapter's ManualControlInput.
func (s *ServerV3) StreamManualControl(stream grpc.ClientStreamingServer[edgev3.StreamManualControlRequest, edgev3.StreamManualControlResponse]) error {
	var accepted int64
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&edgev3.StreamManualControlResponse{AcceptedInputs: accepted})
		}
		if err != nil {
			return err
		}
		in := req.GetInput()
		roll, pitch, yaw, throttle := in.GetRoll(), in.GetPitch(), in.GetYaw(), in.GetThrottle()
		frame := &domains.ManualControlInput{SN: req.GetAsset().GetSn(), Roll: &roll, Pitch: &pitch, Yaw: &yaw, Throttle: &throttle}
		if in.GimbalPitch != nil {
			g := in.GetGimbalPitch()
			frame.GimbalPitch = &g
		}
		if _, err := s.adapter.ManualControlInput(stream.Context(), frame); err != nil {
			s.log.Error("manual control input failed", "sn", frame.SN, "error", err)
		}
		accepted++
	}
}

// StreamDetections streams the adapter's detections.
func (s *ServerV3) StreamDetections(req *edgev3.StreamDetectionsRequest, stream grpc.ServerStreamingServer[edgev3.StreamDetectionsResponse]) error {
	in := &domains.GetDetectionsRequest{SN: req.GetAsset().GetSn()}
	if url := req.GetStreamUrl(); url != "" {
		in.StreamURL = &url
	}
	return s.adapter.GetDetections(stream.Context(), in, func(d *domains.DetectionResult) error {
		return stream.Send(&edgev3.StreamDetectionsResponse{
			Asset:      req.GetAsset(),
			StreamUrl:  req.GetStreamUrl(),
			ObservedAt: timestamppb.Now(),
			Detections: detectionsV3(d),
		})
	})
}

// commandResult maps the SDK's CommandResult onto the v3 shape. A not-implemented result is
// REJECTED with NotSupportedCode, never an aborted call, so the platform can tell "this asset
// cannot do that" from "it tried and failed".
func commandResult(commandID, commandExecutionID string, r *domains.CommandResult) *capabilityv3.CommandResult {
	out := &capabilityv3.CommandResult{CommandExecutionId: commandExecutionID, CommandId: commandID}
	switch {
	case r.IsSuccess():
		out.State = capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED
	case r.IsNotImplemented():
		out.State = capabilityv3.CommandState_COMMAND_STATE_REJECTED
		out.Error = &commonv3.Error{
			Category:   commonv3.ErrorCategory_ERROR_CATEGORY_INVALID_ARGUMENT,
			Code:       NotSupportedCode,
			Message:    strings.TrimSpace(commandID + " is not supported by this adapter"),
			OccurredAt: timestamppb.Now(),
		}
	default:
		msg := ""
		if r != nil {
			msg = r.Message
		}
		if msg == "" {
			msg = commandID + " failed"
		}
		out.State = capabilityv3.CommandState_COMMAND_STATE_FAILED
		out.Error = &commonv3.Error{
			Category:   commonv3.ErrorCategory_ERROR_CATEGORY_ASSET,
			Message:    msg,
			OccurredAt: timestamppb.Now(),
		}
	}
	return out
}

// builtIn routes a built-in command id onto the typed EdgeAdapter method that implements it.
// Params follow the catalog's JSON Schemas (zqnt-protos v3/COMMAND_CATALOG.md), the same as the
// Java SDK's BuiltInCommandDispatch and the Python SDK's typed dispatch. The second return value
// is false for an id that is not a built-in, which then goes to SendCustomCommand.
func builtIn(a adapter.EdgeAdapter, sn, tid, id string, p map[string]any) (func(context.Context) (*domains.CommandResult, error), bool) {
	switch id {
	case "flight.takeoff":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.TakeOff(ctx, &domains.TakeOffRequest{SN: sn, TID: tid, Coordinates: coordinates(p)})
		}, true
	case "navigation.go_to":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.GoTo(ctx, &domains.GoToRequest{SN: sn, TID: tid, Coordinates: coordinates(p)})
		}, true
	case "flight.return_to_home":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.ReturnToHome(ctx, &domains.ReturnToHomeRequest{SN: sn, TID: tid, Altitude: float32Ptr(p, "altitude")})
		}, true
	case "flight.manual.enter":
		return func(ctx context.Context) (*domains.CommandResult, error) { return a.EnterManualControl(ctx, sn) }, true
	case "flight.manual.exit":
		return func(ctx context.Context) (*domains.CommandResult, error) { return a.ExitManualControl(ctx, sn) }, true
	case "gimbal.look_at":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			c := coordinates(p)
			return a.LookAt(ctx, &domains.LookAtRequest{SN: sn, TID: tid, Lat: c.Lat, Lon: c.Lon, Alt: float32(c.Alt),
				PayloadIndex: stringPtr(p, "payloadIndex"), Locked: boolPtr(p, "locked")})
		}, true
	case "gimbal.tracking":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.EnableGimbalTracking(ctx, sn, boolValue(p, "enabled"))
		}, true
	case "camera.take_photo", "camera.capture_photo":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.TakePhoto(ctx, &domains.TakePhotoRequest{SN: sn, TID: tid})
		}, true
	case "camera.change_lens":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.ChangeLens(ctx, &domains.ChangeLensRequest{SN: sn, TID: tid, Lens: stringPtr(p, "lens")})
		}, true
	case "camera.change_zoom":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			req := &domains.ChangeZoomRequest{SN: sn, TID: tid, Lens: stringPtr(p, "lens")}
			if z, ok := number(p, "zoom"); ok {
				zoom := int32(z)
				req.Zoom = &zoom
			}
			return a.ChangeZoom(ctx, req)
		}, true
	case "stream.start":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.StartLiveStream(ctx, &domains.LiveStreamStartRequest{SN: sn, TID: tid,
				VideoID: stringValue(p, "videoId"), StreamServer: stringValue(p, "streamServer"), VideoType: stringValue(p, "videoType")})
		}, true
	case "stream.stop":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.StopLiveStream(ctx, &domains.LiveStreamStopRequest{SN: sn, TID: tid, VideoID: stringValue(p, "videoId")})
		}, true
	case "stream.split_screen":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.LiveStreamSplitScreen(ctx, sn, boolValue(p, "enabled"))
		}, true
	case "dock.open_cover":
		return func(ctx context.Context) (*domains.CommandResult, error) { return a.OpenCover(ctx, sn) }, true
	case "dock.close_cover":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.CloseCover(ctx, sn, boolPtr(p, "force"))
		}, true
	case "dock.start_charging":
		return func(ctx context.Context) (*domains.CommandResult, error) { return a.StartCharging(ctx, sn) }, true
	case "dock.stop_charging":
		return func(ctx context.Context) (*domains.CommandResult, error) { return a.StopCharging(ctx, sn) }, true
	case "asset.reboot":
		return func(ctx context.Context) (*domains.CommandResult, error) { return a.RebootAsset(ctx, sn) }, true
	case "asset.boot_sub_asset":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			if boolValue(p, "enabled") {
				return a.BootUpSubAsset(ctx, sn)
			}
			return a.BootDownSubAsset(ctx, sn)
		}, true
	case "asset.remote_debug":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			if boolValue(p, "enabled") {
				return a.EnterRemoteDebugMode(ctx, sn)
			}
			return a.CloseRemoteDebugMode(ctx, sn)
		}, true
	case "asset.change_ac_mode":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.ChangeACMode(ctx, sn, stringValue(p, "mode"))
		}, true
	case "mission.prepare":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.PrepareTask(ctx, sn, stringValue(p, "taskId"), tid)
		}, true
	case "mission.start":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.StartTask(ctx, sn, stringValue(p, "taskId"), tid)
		}, true
	case "mission.stop":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.StopTask(ctx, sn, stringValue(p, "taskId"), tid)
		}, true
	case "mission.pause":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.PauseTask(ctx, sn, stringValue(p, "taskId"), tid)
		}, true
	case "mission.resume":
		return func(ctx context.Context) (*domains.CommandResult, error) {
			return a.ResumeTask(ctx, sn, stringValue(p, "taskId"), tid)
		}, true
	}
	return nil, false
}

// coordinates reads latitude/longitude/altitude. An absent component is NaN, not 0 -- the
// platform's convention for "not provided" (0,0 is a real place off the coast of Ghana).
func coordinates(p map[string]any) domains.Coordinates {
	c := domains.Coordinates{Lat: math.NaN(), Lon: math.NaN(), Alt: math.NaN()}
	if v, ok := number(p, "latitude"); ok {
		c.Lat = v
	}
	if v, ok := number(p, "longitude"); ok {
		c.Lon = v
	}
	if v, ok := number(p, "altitude"); ok {
		c.Alt = v
	}
	return c
}

func number(p map[string]any, key string) (float64, bool) {
	v, ok := p[key].(float64)
	return v, ok
}

func float32Ptr(p map[string]any, key string) *float32 {
	if v, ok := number(p, key); ok {
		f := float32(v)
		return &f
	}
	return nil
}

func stringValue(p map[string]any, key string) string {
	v, _ := p[key].(string)
	return v
}

func stringPtr(p map[string]any, key string) *string {
	if v, ok := p[key].(string); ok {
		return &v
	}
	return nil
}

func boolValue(p map[string]any, key string) bool {
	v, _ := p[key].(bool)
	return v
}

func boolPtr(p map[string]any, key string) *bool {
	if v, ok := p[key].(bool); ok {
		return &v
	}
	return nil
}

func detectionsV3(d *domains.DetectionResult) []*edgev3.Detection {
	if d == nil {
		return nil
	}
	// The adapter sends one detection per call.
	return []*edgev3.Detection{{
		ObjectId:   d.ObjectID,
		ObjectType: d.ObjectType,
		Confidence: d.Confidence,
		BoundingBox: &edgev3.BoundingBox{X: d.BoundingBox.X, Y: d.BoundingBox.Y,
			Width: d.BoundingBox.Width, Height: d.BoundingBox.Height},
	}}
}
