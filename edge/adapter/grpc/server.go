package adaptergrpc

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"edge-go-sdk/adapter"
	"edge-go-sdk/adapter/domains"
	// zqntpbbuf message types (package _go → alias it)
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"
	// gRPC service stubs (package _gogrpc →alias it)
	zqntgrpc "buf.build/gen/go/zqnt/protos/grpc/go/_gogrpc"
	"edge-go-sdk/internal/protohelpers"

	"google.golang.org/grpc"
)

// Server implements proto.EdgeAdapterServiceServer by delegating each RPC to
// the user-provided EdgeAdapter.
type Server struct {
	zqntgrpc.UnimplementedEdgeAdapterServiceServer
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
	zqntgrpc.RegisterEdgeAdapterServiceServer(gs, s)
}

// ---- helpers ----------------------------------------------------------------

func (s *Server) toEdgeResponse(base *zqntpb.RequestBase, result *domains.CommandResult) *zqntpb.EdgeResponse {
	resp := &zqntpb.EdgeResponse{
		Tid: base.GetTid(),
		Sn:  base.GetSn(),
	}
	if result.Message != "" {
		msg := result.Message
		resp.ResponseMessage = &msg
	}
	if result.IsNotImplemented() {
		hasErr := true
		resp.HasErrors = &hasErr
		resp.Response = &zqntpb.EdgeResponse_Error{
			Error: &zqntpb.GlobalErrorMessage{
				ErrorMessage: result.Message,
				ErrorCode:    zqntpb.ErrorCode_CLIENT_ERROR,
				Timestamp:    protohelpers.Now(),
			},
		}
		s.log.Warn("command not implemented", "message", result.Message, "sn", base.GetSn())
		return resp
	}
	if result.IsSuccess() {
		hasErr := false
		resp.HasErrors = &hasErr
	} else {
		hasErr := true
		resp.HasErrors = &hasErr
		resp.Response = &zqntpb.EdgeResponse_Error{
			Error: &zqntpb.GlobalErrorMessage{
				ErrorMessage: result.Message,
				ErrorCode:    zqntpb.ErrorCode_ASSET_ERROR,
				Timestamp:    protohelpers.Now(),
			},
		}
	}
	return resp
}

func (s *Server) toErrorResponse(base *zqntpb.RequestBase, err error) *zqntpb.EdgeResponse {
	s.log.Error("error processing command", "sn", base.GetSn(), "tid", base.GetTid(), "error", err)
	hasErr := true
	msg := err.Error()
	return &zqntpb.EdgeResponse{
		HasErrors:       &hasErr,
		Tid:             base.GetTid(),
		Sn:              base.GetSn(),
		ResponseMessage: &msg,
		Response: &zqntpb.EdgeResponse_Error{
			Error: &zqntpb.GlobalErrorMessage{
				ErrorMessage: err.Error(),
				ErrorCode:    zqntpb.ErrorCode_SYSTEM_ERROR,
				Timestamp:    protohelpers.Now(),
			},
		},
	}
}

// ---- Unary RPCs -------------------------------------------------------------

func (s *Server) TakeOff(ctx context.Context, req *zqntpb.EdgeTakeOffRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("TakeOff", "sn", req.Base.GetSn())
	result, err := s.adapter.TakeOff(ctx, s.mapper.MapTakeOffRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) ReturnToHome(ctx context.Context, req *zqntpb.EdgeReturnToHomeRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("ReturnToHome", "sn", req.Base.GetSn())
	result, err := s.adapter.ReturnToHome(ctx, s.mapper.MapReturnToHomeRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) GoTo(ctx context.Context, req *zqntpb.EdgeGoToRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("GoTo", "sn", req.Base.GetSn())
	result, err := s.adapter.GoTo(ctx, s.mapper.MapGoToRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) EnterManualControl(ctx context.Context, req *zqntpb.EdgeManualControlRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("EnterManualControl", "sn", req.Base.GetSn())
	result, err := s.adapter.EnterManualControl(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) ExitManualControl(ctx context.Context, req *zqntpb.EdgeManualControlRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("ExitManualControl", "sn", req.Base.GetSn())
	result, err := s.adapter.ExitManualControl(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

// ManualControlInput handles client-streaming manual control inputs.
func (s *Server) ManualControlInput(stream grpc.ClientStreamingServer[zqntpb.EdgeManualControlInputRequest, zqntpb.EdgeResponse]) error {
	s.log.Info("ManualControlInput stream started")
	var sn string

	for {
		req, err := stream.Recv()
		if err != nil {
			// EOF = client done sending
			if err == io.EOF {
				tid := protohelpers.GenerateTID()
				base := &zqntpb.RequestBase{Tid: tid, Sn: sn, Timestamp: protohelpers.Now()}
				result := domains.SuccessWithTID("manual control input session completed", tid, sn)
				return stream.SendAndClose(s.toEdgeResponse(base, result))
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

func (s *Server) LookAt(ctx context.Context, req *zqntpb.EdgeLookAtRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("LookAt", "sn", req.Base.GetSn())
	result, err := s.adapter.LookAt(ctx, s.mapper.MapLookAtRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) TakePhoto(ctx context.Context, req *zqntpb.EdgeTakePhotoRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("TakePhoto", "sn", req.Base.GetSn())
	result, err := s.adapter.TakePhoto(ctx, s.mapper.MapTakePhotoRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) EnableGimbalTracking(ctx context.Context, req *zqntpb.EdgeEnableGimbalTrackingRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("EnableGimbalTracking", "sn", req.Base.GetSn())
	result, err := s.adapter.EnableGimbalTracking(ctx, req.Base.GetSn(), req.Enabled)
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

// GetDetections handles server-streaming detection results.
func (s *Server) GetDetections(req *zqntpb.EdgeGetDetectionsRequest, stream grpc.ServerStreamingServer[zqntpb.EdgeDetectionResponse]) error {
	s.log.Info("GetDetections", "sn", req.Base.GetSn())
	domainReq := s.mapper.MapGetDetectionsRequest(req)

	return s.adapter.GetDetections(stream.Context(), domainReq, func(det *domains.DetectionResult) error {
		if det == nil {
			return nil
		}
		return stream.Send(&zqntpb.EdgeDetectionResponse{
			Base: req.Base,
			Detections: []*zqntpb.EdgeDetectionResponse_DetectionResult{
				{
					ObjectId:   det.ObjectID,
					ObjectType: det.ObjectType,
					Confidence: det.Confidence,
					BoundingBox: &zqntpb.EdgeDetectionResponse_DetectionResult_BoundingBox{
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

func (s *Server) OpenCover(ctx context.Context, req *zqntpb.EdgeOpenCoverRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("OpenCover", "sn", req.Base.GetSn())
	result, err := s.adapter.OpenCover(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) CloseCover(ctx context.Context, req *zqntpb.EdgeCloseCoverRequest) (*zqntpb.EdgeResponse, error) {
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
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) StartCharging(ctx context.Context, req *zqntpb.EdgeStartChargingRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("StartCharging", "sn", req.Base.GetSn())
	result, err := s.adapter.StartCharging(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) StopCharging(ctx context.Context, req *zqntpb.EdgeStopChargingRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("StopCharging", "sn", req.Base.GetSn())
	result, err := s.adapter.StopCharging(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) RebootAsset(ctx context.Context, req *zqntpb.EdgeRebootAssetRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("RebootAsset", "sn", req.Base.GetSn())
	result, err := s.adapter.RebootAsset(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) BootUpSubAsset(ctx context.Context, req *zqntpb.EdgeBootSubAssetRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("BootUpSubAsset", "sn", req.Base.GetSn())
	result, err := s.adapter.BootUpSubAsset(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) BootDownSubAsset(ctx context.Context, req *zqntpb.EdgeBootSubAssetRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("BootDownSubAsset", "sn", req.Base.GetSn())
	result, err := s.adapter.BootDownSubAsset(ctx, req.Base.GetSn())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) EnterOrCloseRemoteDebugMode(ctx context.Context, req *zqntpb.EdgeRemoteDebugModeRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("EnterOrCloseRemoteDebugMode", "sn", req.Base.GetSn(), "enabled", req.Enabled)
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
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) ChangeAcMode(ctx context.Context, req *zqntpb.EdgeChangeAcModeRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("ChangeAcMode", "sn", req.Base.GetSn())
	result, err := s.adapter.ChangeACMode(ctx, req.Base.GetSn(), req.Mode.String())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) StartLiveStream(ctx context.Context, req *zqntpb.EdgeStartLiveStreamRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("StartLiveStream", "sn", req.Base.GetSn())
	result, err := s.adapter.StartLiveStream(ctx, s.mapper.MapStartLiveStreamRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) StopLiveStream(ctx context.Context, req *zqntpb.EdgeStopLiveStreamRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("StopLiveStream", "sn", req.Base.GetSn())
	result, err := s.adapter.StopLiveStream(ctx, s.mapper.MapStopLiveStreamRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) ChangeLens(ctx context.Context, req *zqntpb.EdgeChangeCameraLensRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("ChangeLens", "sn", req.Base.GetSn())
	result, err := s.adapter.ChangeLens(ctx, s.mapper.MapChangeLensRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) ChangeZoom(ctx context.Context, req *zqntpb.EdgeChangeCameraZoomRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("ChangeZoom", "sn", req.Base.GetSn())
	result, err := s.adapter.ChangeZoom(ctx, s.mapper.MapChangeZoomRequest(req))
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) GetCapabilities(ctx context.Context, req *zqntpb.EdgeGetCapabilitiesRequest) (*zqntpb.EdgeGetCapabilitiesResponse, error) {
	s.log.Info("GetCapabilities", "sn", req.Sn)
	caps, err := s.adapter.GetCapabilities(ctx, req.Sn)
	if err != nil {
		errMsg := fmt.Sprintf("error getting capabilities: %s", err.Error())
		return &zqntpb.EdgeGetCapabilitiesResponse{
			Error: &zqntpb.GlobalErrorMessage{
				ErrorMessage: errMsg,
				ErrorCode:    zqntpb.ErrorCode_SYSTEM_ERROR,
				Timestamp:    protohelpers.Now(),
			},
		}, nil
	}

	protoCaps := make([]*zqntpb.Capability, 0, len(caps.Capabilities))
	for _, c := range caps.Capabilities {
		cap := &zqntpb.Capability{
			Command:     c.Command,
			Description: c.Description,
			Available:   c.Available,
			Metadata:    c.Metadata,
		}
		cap.UnavailableReason = c.UnavailableReason
		protoCaps = append(protoCaps, cap)
	}

	return &zqntpb.EdgeGetCapabilitiesResponse{
		Capabilities: &zqntpb.CurrentCapabilities{
			AssetSn:      caps.SN,
			Capabilities: protoCaps,
			Timestamp:    protohelpers.Now(),
		},
	}, nil
}

func (s *Server) StartTask(ctx context.Context, req *zqntpb.EdgeStartTaskRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("StartTask", "sn", req.Base.GetSn(), "taskId", req.TaskId)
	result, err := s.adapter.StartTask(ctx, req.TaskId, req.Base.GetTid())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) StopTask(ctx context.Context, req *zqntpb.EdgeStopTaskRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Warn("StopTask", "sn", req.Base.GetSn(), "taskId", req.TaskId)
	result, err := s.adapter.StopTask(ctx, req.TaskId)
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

func (s *Server) PrepareTask(ctx context.Context, req *zqntpb.EdgePrepareTaskRequest) (*zqntpb.EdgeResponse, error) {
	s.log.Info("PrepareTask", "sn", req.Base.GetSn(), "taskId", req.TaskId)
	result, err := s.adapter.PrepareTask(ctx, req.TaskId, req.Base.GetTid())
	if err != nil {
		return s.toErrorResponse(req.Base, err), nil
	}
	return s.toEdgeResponse(req.Base, result), nil
}

