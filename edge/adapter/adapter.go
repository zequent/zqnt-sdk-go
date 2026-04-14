// Package adapter defines the EdgeAdapter interface that SDK consumers implement
// to expose hardware control operations over gRPC.
package adapter

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// EdgeAdapter is the interface SDK consumers implement to control their hardware.
//
// Embed [UnimplementedEdgeAdapter] in your concrete struct to get default
// NOT_IMPLEMENTED responses for every method, then override only the commands
// your asset supports. This pattern mirrors the generated gRPC server pattern.
//
// Example:
//
//	type MyDroneAdapter struct {
//	    adapter.UnimplementedEdgeAdapter
//	    drone *hardware.Drone
//	}
//
//	func (a *MyDroneAdapter) TakeOff(ctx context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
//	    if err := a.drone.TakeOff(req.Coordinates.Alt); err != nil {
//	        return domains.Error(err.Error(), req.SN), nil
//	    }
//	    return domains.SuccessWithTID("takeOff accepted", req.TID, req.SN), nil
//	}
type EdgeAdapter interface {
	TakeOff(ctx context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error)
	ReturnToHome(ctx context.Context, req *domains.ReturnToHomeRequest) (*domains.CommandResult, error)
	GoTo(ctx context.Context, req *domains.GoToRequest) (*domains.CommandResult, error)

	EnterManualControl(ctx context.Context, sn string) (*domains.CommandResult, error)
	ExitManualControl(ctx context.Context, sn string) (*domains.CommandResult, error)
	ManualControlInput(ctx context.Context, input *domains.ManualControlInput) (*domains.CommandResult, error)

	OpenCover(ctx context.Context, sn string) (*domains.CommandResult, error)
	CloseCover(ctx context.Context, sn string, force *bool) (*domains.CommandResult, error)
	StartCharging(ctx context.Context, sn string) (*domains.CommandResult, error)
	StopCharging(ctx context.Context, sn string) (*domains.CommandResult, error)

	RebootAsset(ctx context.Context, sn string) (*domains.CommandResult, error)
	BootUpSubAsset(ctx context.Context, sn string) (*domains.CommandResult, error)
	BootDownSubAsset(ctx context.Context, sn string) (*domains.CommandResult, error)

	LookAt(ctx context.Context, req *domains.LookAtRequest) (*domains.CommandResult, error)
	TakePhoto(ctx context.Context, req *domains.TakePhotoRequest) (*domains.CommandResult, error)
	ChangeLens(ctx context.Context, req *domains.ChangeLensRequest) (*domains.CommandResult, error)
	ChangeZoom(ctx context.Context, req *domains.ChangeZoomRequest) (*domains.CommandResult, error)
	EnableGimbalTracking(ctx context.Context, sn string, enabled bool) (*domains.CommandResult, error)

	StartLiveStream(ctx context.Context, req *domains.LiveStreamStartRequest) (*domains.CommandResult, error)
	StopLiveStream(ctx context.Context, req *domains.LiveStreamStopRequest) (*domains.CommandResult, error)

	EnterRemoteDebugMode(ctx context.Context, sn string) (*domains.CommandResult, error)
	CloseRemoteDebugMode(ctx context.Context, sn string) (*domains.CommandResult, error)
	ChangeACMode(ctx context.Context, sn, mode string) (*domains.CommandResult, error)

	GetCapabilities(ctx context.Context, sn string) (*domains.CurrentCapabilities, error)

	// GetDetections is a server-streaming operation.
	// The adapter calls send for each detection frame until ctx is cancelled or
	// the stream ends. Returning a non-nil error terminates the stream.
	GetDetections(ctx context.Context, req *domains.GetDetectionsRequest, send func(*domains.DetectionResult) error) error

	StartTask(ctx context.Context, taskID, tid string) (*domains.CommandResult, error)
	StopTask(ctx context.Context, taskID string) (*domains.CommandResult, error)
	PrepareTask(ctx context.Context, taskID, tid string) (*domains.CommandResult, error)
}

// UnimplementedEdgeAdapter provides NOT_IMPLEMENTED default implementations for all
// EdgeAdapter methods. Embed this in your concrete type and override as needed.
type UnimplementedEdgeAdapter struct{}

func (UnimplementedEdgeAdapter) TakeOff(_ context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("takeOff is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) ReturnToHome(_ context.Context, req *domains.ReturnToHomeRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("returnToHome is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) GoTo(_ context.Context, req *domains.GoToRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("goTo is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) EnterManualControl(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("enterManualControl is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) ExitManualControl(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("exitManualControl is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) ManualControlInput(_ context.Context, input *domains.ManualControlInput) (*domains.CommandResult, error) {
	return domains.NotImplemented("manualControlInput is not implemented for this asset", input.SN), nil
}

func (UnimplementedEdgeAdapter) OpenCover(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("openCover is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) CloseCover(_ context.Context, sn string, _ *bool) (*domains.CommandResult, error) {
	return domains.NotImplemented("closeCover is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) StartCharging(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("startCharging is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) StopCharging(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("stopCharging is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) RebootAsset(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("rebootAsset is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) BootUpSubAsset(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("bootUpSubAsset is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) BootDownSubAsset(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("bootDownSubAsset is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) LookAt(_ context.Context, req *domains.LookAtRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("lookAt is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) TakePhoto(_ context.Context, req *domains.TakePhotoRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("takePhoto is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) ChangeLens(_ context.Context, req *domains.ChangeLensRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("changeLens is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) ChangeZoom(_ context.Context, req *domains.ChangeZoomRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("changeZoom is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) EnableGimbalTracking(_ context.Context, sn string, _ bool) (*domains.CommandResult, error) {
	return domains.NotImplemented("enableGimbalTracking is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) StartLiveStream(_ context.Context, req *domains.LiveStreamStartRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("startLiveStream is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) StopLiveStream(_ context.Context, req *domains.LiveStreamStopRequest) (*domains.CommandResult, error) {
	return domains.NotImplemented("stopLiveStream is not implemented for this asset", req.SN), nil
}

func (UnimplementedEdgeAdapter) EnterRemoteDebugMode(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("enterRemoteDebugMode is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) CloseRemoteDebugMode(_ context.Context, sn string) (*domains.CommandResult, error) {
	return domains.NotImplemented("closeRemoteDebugMode is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) ChangeACMode(_ context.Context, sn, _ string) (*domains.CommandResult, error) {
	return domains.NotImplemented("changeAcMode is not implemented for this asset", sn), nil
}

func (UnimplementedEdgeAdapter) GetCapabilities(_ context.Context, sn string) (*domains.CurrentCapabilities, error) {
	return domains.EmptyCapabilities(sn), nil
}

func (UnimplementedEdgeAdapter) GetDetections(_ context.Context, _ *domains.GetDetectionsRequest, _ func(*domains.DetectionResult) error) error {
	return nil
}

func (UnimplementedEdgeAdapter) StartTask(_ context.Context, taskID, _ string) (*domains.CommandResult, error) {
	return domains.NotImplemented("startTask is not implemented for this asset", taskID), nil
}

func (UnimplementedEdgeAdapter) StopTask(_ context.Context, taskID string) (*domains.CommandResult, error) {
	return domains.NotImplemented("stopTask is not implemented for this asset", taskID), nil
}

func (UnimplementedEdgeAdapter) PrepareTask(_ context.Context, taskID, _ string) (*domains.CommandResult, error) {
	return domains.NotImplemented("prepareTask is not implemented for this asset", taskID), nil
}
