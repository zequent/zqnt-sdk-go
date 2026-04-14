// Package adaptergrpc provides the gRPC server implementation that bridges
// incoming EdgeAdapterService RPC calls to the user-provided EdgeAdapter interface.
//
// This package requires proto code generation. Run `make proto` before building.
package adaptergrpc

import (
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	// zqntpbbuf message types (package _go → alias it)
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"
)

// Mapper converts between proto request/response messages and domain structs.
type Mapper struct{}

func (m *Mapper) MapCoordinates(p *zqntpb.Coordinates) domains.Coordinates {
	if p == nil {
		return domains.Coordinates{}
	}
	return domains.Coordinates{Lat: p.Latitude, Lon: p.Longitude, Alt: p.Altitude}
}

func (m *Mapper) MapTakeOffRequest(r *zqntpb.EdgeTakeOffRequest) *domains.TakeOffRequest {
	if r == nil {
		return nil
	}
	return &domains.TakeOffRequest{
		SN:          r.Base.GetSn(),
		TID:         r.Base.GetTid(),
		Coordinates: m.MapCoordinates(r.Request),
	}
}

func (m *Mapper) MapGoToRequest(r *zqntpb.EdgeGoToRequest) *domains.GoToRequest {
	if r == nil {
		return nil
	}
	return &domains.GoToRequest{
		SN:          r.Base.GetSn(),
		TID:         r.Base.GetTid(),
		Coordinates: m.MapCoordinates(r.Request),
	}
}

func (m *Mapper) MapReturnToHomeRequest(r *zqntpb.EdgeReturnToHomeRequest) *domains.ReturnToHomeRequest {
	if r == nil {
		return nil
	}
	req := &domains.ReturnToHomeRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.Request != nil && r.Request.Altitude != nil {
		v := r.Request.GetAltitude()
		req.Altitude = &v
	}
	return req
}

func (m *Mapper) MapLookAtRequest(r *zqntpb.EdgeLookAtRequest) *domains.LookAtRequest {
	if r == nil {
		return nil
	}
	req := &domains.LookAtRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.Request != nil {
		req.Lat = r.Request.Latitude
		req.Lon = r.Request.Longitude
		req.Alt = float32(r.Request.Altitude)
	}
	if r.PayloadIndex != nil {
		v := r.GetPayloadIndex()
		req.PayloadIndex = &v
	}
	if r.Locked != nil {
		v := r.GetLocked()
		req.Locked = &v
	}
	return req
}

func (m *Mapper) MapTakePhotoRequest(r *zqntpb.EdgeTakePhotoRequest) *domains.TakePhotoRequest {
	if r == nil {
		return nil
	}
	return &domains.TakePhotoRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
}

func (m *Mapper) MapManualControlInput(r *zqntpb.EdgeManualControlInputRequest) *domains.ManualControlInput {
	if r == nil {
		return nil
	}
	input := &domains.ManualControlInput{SN: r.Base.GetSn()}
	if r.Request != nil {
		if r.Request.Roll != nil {
			v := r.Request.GetRoll()
			input.Roll = &v
		}
		if r.Request.Pitch != nil {
			v := r.Request.GetPitch()
			input.Pitch = &v
		}
		if r.Request.Yaw != nil {
			v := r.Request.GetYaw()
			input.Yaw = &v
		}
		if r.Request.Throttle != nil {
			v := r.Request.GetThrottle()
			input.Throttle = &v
		}
		if r.Request.GimbalPitch != nil {
			v := r.Request.GetGimbalPitch()
			input.GimbalPitch = &v
		}
	}
	return input
}

func (m *Mapper) MapChangeLensRequest(r *zqntpb.EdgeChangeCameraLensRequest) *domains.ChangeLensRequest {
	if r == nil {
		return nil
	}
	req := &domains.ChangeLensRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.Request != nil && r.Request.Lens != nil {
		v := r.Request.GetLens()
		req.Lens = &v
	}
	return req
}

func (m *Mapper) MapChangeZoomRequest(r *zqntpb.EdgeChangeCameraZoomRequest) *domains.ChangeZoomRequest {
	if r == nil {
		return nil
	}
	req := &domains.ChangeZoomRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.Request != nil {
		if r.Request.Lens != nil {
			v := r.Request.GetLens()
			req.Lens = &v
		}
		if r.Request.Zoom != nil {
			v := r.Request.GetZoom()
			req.Zoom = &v
		}
	}
	return req
}

func (m *Mapper) MapStartLiveStreamRequest(r *zqntpb.EdgeStartLiveStreamRequest) *domains.LiveStreamStartRequest {
	if r == nil {
		return nil
	}
	req := &domains.LiveStreamStartRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.Request != nil {
		req.VideoID = r.Request.VideoId
		req.StreamServer = r.Request.StreamServer
		req.VideoType = r.Request.StreamType.String()
	}
	return req
}

func (m *Mapper) MapStopLiveStreamRequest(r *zqntpb.EdgeStopLiveStreamRequest) *domains.LiveStreamStopRequest {
	if r == nil {
		return nil
	}
	req := &domains.LiveStreamStopRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.Request != nil {
		req.VideoID = r.Request.VideoId
	}
	return req
}

func (m *Mapper) MapGetDetectionsRequest(r *zqntpb.EdgeGetDetectionsRequest) *domains.GetDetectionsRequest {
	if r == nil {
		return nil
	}
	req := &domains.GetDetectionsRequest{
		SN:  r.Base.GetSn(),
		TID: r.Base.GetTid(),
	}
	if r.StreamUrl != nil {
		v := r.GetStreamUrl()
		req.StreamURL = &v
	}
	return req
}
