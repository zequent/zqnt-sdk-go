package adaptergrpc

import (
	"context"
	"io"
	"log/slog"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	detectionpb "github.com/Zequent/zqnt-edge-sdk-go/gen/common/detection/proto"
	commonpb "github.com/Zequent/zqnt-edge-sdk-go/gen/common/proto"
	devicecontrolpb "github.com/Zequent/zqnt-edge-sdk-go/gen/devicecontrol/contracts/proto"
	edgepb "github.com/Zequent/zqnt-edge-sdk-go/gen/edge/sdk/proto"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Server implements edgepb.EdgeAdapterServiceServer by delegating each RPC to the
// user-provided EdgeAdapter. RPCs with no equivalent on the EdgeAdapter interface
// (StartRecording/StopRecording/LiveStreamSplitScreen/SendCustomCommand/PauseTask/ResumeTask/
// RegisterAsset/DeregisterAsset -- all new on the current schema, no wire contract existed for
// them before) are intentionally left unimplemented, inherited from the embedded
// UnimplementedEdgeAdapterServiceServer (returns a clean codes.Unimplemented) -- matches this
// SDK's own standing convention: "only the commands a device supports need to be overridden;
// everything else defaults to NOT_IMPLEMENTED" (see CLAUDE.md / edge-python-sdk's identical
// pattern). Adding real support for those is a separate, deliberate follow-up, not done here.
type Server struct {
	edgepb.UnimplementedEdgeAdapterServiceServer
	adapter adapter.EdgeAdapter
	mapper  *Mapper
	log     *slog.Logger
}

// NewServer creates a new Server wrapping the given EdgeAdapter.
func NewServer(a adapter.EdgeAdapter, log *slog.Logger) *Server {
	return &Server{adapter: a, mapper: &Mapper{}, log: log}
}

// RegisterWith registers this server with the given gRPC server instance.
func (s *Server) RegisterWith(gs *grpc.Server) {
	edgepb.RegisterEdgeAdapterServiceServer(gs, s)
}

// ---- helpers ----------------------------------------------------------------

func (s *Server) toCommandResponse(base *commonpb.RequestBase, result *domains.CommandResult) *devicecontrolpb.CommandResponse {
	resp := &devicecontrolpb.CommandResponse{
		Meta: &commonpb.ResponseMeta{Tid: base.GetTid(), Sn: base.GetSn(), Timestamp: protohelpers.Now()},
	}
	if result.IsNotImplemented() {
		hasErr := true
		resp.HasErrors = &hasErr
		resp.Response = &devicecontrolpb.CommandResponse_Error{
			Error: &commonpb.GlobalErrorMessage{
				ErrorMessage: result.Message,
				ErrorCode:    commonpb.ErrorCode_ERROR_CODE_CLIENT,
				Timestamp:    protohelpers.Now(),
			},
		}
		s.log.Warn("command not implemented", "message", result.Message, "sn", base.GetSn())
		return resp
	}
	if result.IsSuccess() {
		hasErr := false
		resp.HasErrors = &hasErr
		resp.Response = &devicecontrolpb.CommandResponse_Empty{Empty: &emptypb.Empty{}}
	} else {
		hasErr := true
		resp.HasErrors = &hasErr
		resp.Response = &devicecontrolpb.CommandResponse_Error{
			Error: &commonpb.GlobalErrorMessage{
				ErrorMessage: result.Message,
				ErrorCode:    commonpb.ErrorCode_ERROR_CODE_ASSET,
				Timestamp:    protohelpers.Now(),
			},
		}
	}
	return resp
}

func (s *Server) toErrorResponse(base *commonpb.RequestBase, err error) *devicecontrolpb.CommandResponse {
	s.log.Error("error processing command", "sn", base.GetSn(), "tid", base.GetTid(), "error", err)
	hasErr := true
	return &devicecontrolpb.CommandResponse{
		HasErrors: &hasErr,
		Meta:      &commonpb.ResponseMeta{Tid: base.GetTid(), Sn: base.GetSn(), Timestamp: protohelpers.Now()},
		Response: &devicecontrolpb.CommandResponse_Error{
			Error: &commonpb.GlobalErrorMessage{
				ErrorMessage: err.Error(),
				ErrorCode:    commonpb.ErrorCode_ERROR_CODE_SYSTEM,
				Timestamp:    protohelpers.Now(),
			},
		},
	}
}

// ---- Unary RPCs -------------------------------------------------------------

func (s *Server) TakeOff(ctx context.Context, req *devicecontrolpb.CoordinateCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("TakeOff", "sn", req.Base.GetSn())
	result, err := s.adapter.TakeOff(ctx, s.mapper.MapTakeOffRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) ReturnToHome(ctx context.Context, req *devicecontrolpb.ReturnToHomeCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("ReturnToHome", "sn", req.Base.GetSn())
	result, err := s.adapter.ReturnToHome(ctx, s.mapper.MapReturnToHomeRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) GoTo(ctx context.Context, req *devicecontrolpb.CoordinateCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("GoTo", "sn", req.Base.GetSn())
	result, err := s.adapter.GoTo(ctx, s.mapper.MapGoToRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) EnterManualControl(ctx context.Context, req *devicecontrolpb.ManualControlCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("EnterManualControl", "sn", req.Base.GetSn())
	result, err := s.adapter.EnterManualControl(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) ExitManualControl(ctx context.Context, req *devicecontrolpb.ManualControlCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("ExitManualControl", "sn", req.Base.GetSn())
	result, err := s.adapter.ExitManualControl(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

// ManualControlInput handles client-streaming manual control inputs.
func (s *Server) ManualControlInput(stream grpc.ClientStreamingServer[devicecontrolpb.ManualControlInputCommandRequest, devicecontrolpb.CommandResponse]) error {
	s.log.Info("ManualControlInput stream started")
	var sn string

	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				tid := protohelpers.GenerateTID()
				base := &commonpb.RequestBase{Tid: tid, Sn: sn, Timestamp: protohelpers.Now()}
				result := domains.SuccessWithTID("manual control input session completed", tid, sn)
				return stream.SendAndClose(s.toCommandResponse(base, result))
			}
			s.log.Error("ManualControlInput stream error", "sn", sn, "error", err)
			return err
		}

		input := s.mapper.MapManualControlInput(req)
		if sn == "" {
			sn = input.SN
			s.log.Info("ManualControlInput stream SN identified", "sn", sn)
		}
		if _, adapterErr := s.adapter.ManualControlInput(stream.Context(), input); adapterErr != nil {
			s.log.Error("ManualControlInput adapter error", "sn", sn, "error", adapterErr)
		}
	}
}

func (s *Server) LookAt(ctx context.Context, req *devicecontrolpb.LookAtCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("LookAt", "sn", req.Base.GetSn())
	result, err := s.adapter.LookAt(ctx, s.mapper.MapLookAtRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) CapturePhoto(ctx context.Context, req *devicecontrolpb.EmptyCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("CapturePhoto", "sn", req.Base.GetSn())
	result, err := s.adapter.TakePhoto(ctx, s.mapper.MapTakePhotoRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) EnableGimbalTracking(ctx context.Context, req *devicecontrolpb.ToggleCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("EnableGimbalTracking", "sn", req.Base.GetSn())
	result, err := s.adapter.EnableGimbalTracking(ctx, req.Base.GetSn(), req.Enabled)
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

// GetDetections handles server-streaming detection results.
func (s *Server) GetDetections(req *detectionpb.DetectionStreamRequest, stream grpc.ServerStreamingServer[detectionpb.DetectionBatch]) error {
	s.log.Info("GetDetections", "sn", req.Base.GetSn())
	domainReq := s.mapper.MapGetDetectionsRequest(req)

	return s.adapter.GetDetections(stream.Context(), domainReq, func(det *domains.DetectionResult) error {
		if det == nil {
			return nil
		}
		return stream.Send(&detectionpb.DetectionBatch{
			Base: req.Base,
			Detections: []*detectionpb.DetectionResult{
				{
					ObjectId:   &det.ObjectID,
					ObjectType: &det.ObjectType,
					Confidence: &det.Confidence,
					BoundingBox: &detectionpb.BoundingBox{
						X:      det.BoundingBox.X,
						Y:      det.BoundingBox.Y,
						Width:  det.BoundingBox.Width,
						Height: det.BoundingBox.Height,
					},
				},
			},
		})
	})
}

func (s *Server) OpenCover(ctx context.Context, req *devicecontrolpb.EmptyCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("OpenCover", "sn", req.Base.GetSn())
	result, err := s.adapter.OpenCover(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) CloseCover(ctx context.Context, req *devicecontrolpb.CloseCoverCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("CloseCover", "sn", req.Base.GetSn())
	var force *bool
	if req.Force != nil {
		v := req.GetForce()
		force = &v
	}
	result, err := s.adapter.CloseCover(ctx, req.Base.GetSn(), force)
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) StartCharging(ctx context.Context, req *devicecontrolpb.EmptyCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("StartCharging", "sn", req.Base.GetSn())
	result, err := s.adapter.StartCharging(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) StopCharging(ctx context.Context, req *devicecontrolpb.EmptyCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("StopCharging", "sn", req.Base.GetSn())
	result, err := s.adapter.StopCharging(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) RebootAsset(ctx context.Context, req *devicecontrolpb.EmptyCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("RebootAsset", "sn", req.Base.GetSn())
	result, err := s.adapter.RebootAsset(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

// BootSubAsset toggles sub-asset boot state -- replaces the old separate BootUpSubAsset/
// BootDownSubAsset RPC pair with one Enabled-toggle RPC, same pattern as SetRemoteDebugMode below.
func (s *Server) BootSubAsset(ctx context.Context, req *devicecontrolpb.ToggleCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("BootSubAsset", "sn", req.Base.GetSn(), "enabled", req.Enabled)
	var result *domains.CommandResult
	var err error
	if req.Enabled {
		result, err = s.adapter.BootUpSubAsset(ctx, req.Base.GetSn())
	} else {
		result, err = s.adapter.BootDownSubAsset(ctx, req.Base.GetSn())
	}
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) SetRemoteDebugMode(ctx context.Context, req *devicecontrolpb.ToggleCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("SetRemoteDebugMode", "sn", req.Base.GetSn(), "enabled", req.Enabled)
	var result *domains.CommandResult
	var err error
	if req.Enabled {
		result, err = s.adapter.EnterRemoteDebugMode(ctx, req.Base.GetSn())
	} else {
		result, err = s.adapter.CloseRemoteDebugMode(ctx, req.Base.GetSn())
	}
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) ChangeAcMode(ctx context.Context, req *devicecontrolpb.ChangeAcModeCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("ChangeAcMode", "sn", req.Base.GetSn())
	result, err := s.adapter.ChangeACMode(ctx, req.Base.GetSn(), req.Mode.String())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) StartLiveStream(ctx context.Context, req *devicecontrolpb.LiveStreamStartCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("StartLiveStream", "sn", req.Base.GetSn())
	result, err := s.adapter.StartLiveStream(ctx, s.mapper.MapStartLiveStreamRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) StopLiveStream(ctx context.Context, req *devicecontrolpb.LiveStreamStopCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("StopLiveStream", "sn", req.Base.GetSn())
	result, err := s.adapter.StopLiveStream(ctx, s.mapper.MapStopLiveStreamRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) ChangeLens(ctx context.Context, req *devicecontrolpb.ChangeCameraLensCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("ChangeLens", "sn", req.Base.GetSn())
	result, err := s.adapter.ChangeLens(ctx, s.mapper.MapChangeLensRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) ChangeZoom(ctx context.Context, req *devicecontrolpb.ChangeCameraZoomCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("ChangeZoom", "sn", req.Base.GetSn())
	result, err := s.adapter.ChangeZoom(ctx, s.mapper.MapChangeZoomRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) GetCapabilities(ctx context.Context, req *devicecontrolpb.AssetCapabilitiesRequest) (*devicecontrolpb.AssetCapabilitiesResponse, error) {
	s.log.Info("GetCapabilities", "sn", req.Sn)
	caps, err := s.adapter.GetCapabilities(ctx, req.Sn)
	if err != nil {
		return &devicecontrolpb.AssetCapabilitiesResponse{
			Error: &commonpb.GlobalErrorMessage{
				ErrorMessage: "error getting capabilities: " + err.Error(),
				ErrorCode:    commonpb.ErrorCode_ERROR_CODE_SYSTEM,
				Timestamp:    protohelpers.Now(),
			},
		}, nil
	}

	protoCaps := make([]*devicecontrolpb.Capability, 0, len(caps.Capabilities))
	for _, c := range caps.Capabilities {
		state := devicecontrolpb.CapabilityState_CAPABILITY_STATE_UNSUPPORTED
		if c.Available {
			state = devicecontrolpb.CapabilityState_CAPABILITY_STATE_AVAILABLE
		}
		protoCaps = append(protoCaps, &devicecontrolpb.Capability{
			CommandId:         c.Command,
			DisplayName:       c.Command,
			Description:       &c.Description,
			UnavailableReason: c.UnavailableReason,
			Metadata:          c.Metadata,
			State:             state,
		})
	}

	return &devicecontrolpb.AssetCapabilitiesResponse{
		Capabilities: &devicecontrolpb.AssetCapabilities{
			AssetSn:      caps.SN,
			AssetType:    caps.AssetType,
			Capabilities: protoCaps,
			Timestamp:    protohelpers.Now(),
		},
	}, nil
}

func (s *Server) PrepareTask(ctx context.Context, req *devicecontrolpb.TaskCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("PrepareTask", "sn", req.Base.GetSn(), "taskId", req.TaskId)
	result, err := s.adapter.PrepareTask(ctx, req.TaskId, req.Base.GetTid())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) StartTask(ctx context.Context, req *devicecontrolpb.TaskCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Info("StartTask", "sn", req.Base.GetSn(), "taskId", req.TaskId)
	result, err := s.adapter.StartTask(ctx, req.TaskId, req.Base.GetTid())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}

func (s *Server) StopTask(ctx context.Context, req *devicecontrolpb.TaskCommandRequest) (*devicecontrolpb.CommandResponse, error) {
	s.log.Warn("StopTask", "sn", req.Base.GetSn(), "taskId", req.TaskId)
	result, err := s.adapter.StopTask(ctx, req.TaskId)
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toCommandResponse(req.Base, result), nil
}
