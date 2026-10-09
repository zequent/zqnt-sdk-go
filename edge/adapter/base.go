package adapter

import (
	"context"
	"math"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
)

// Base is the starting point of an adapter: embed it, register each command once on its
// Registry, and the SDK derives everything else from that -- the advertised capabilities, the v3
// ExecuteCommand dispatch with schema validation, and the v2 typed RPCs, which Base routes onto
// the registered handler of the matching command id.
//
//	type Drone struct{ adapter.Base }
//
//	d := &Drone{}
//	d.MustRegisterCommand("flight.takeoff", takeoffInput, nil, d.takeoff,
//	    adapter.WithCompletion(domains.CompletionAsynchronous, "flight.takeoff.completed"))
//
// ManualControlInput and GetDetections are streams, not commands; override them when the asset
// supports them.
type Base struct {
	Registry
}

// CommandRegistry returns the registry the SDK dispatches commands through.
func (b *Base) CommandRegistry() *Registry { return &b.Registry }

// RegistryAdapter is an adapter whose commands come from a Registry.
type RegistryAdapter interface {
	CommandRegistry() *Registry
}

func (b *Base) run(ctx context.Context, sn, tid, id string, params map[string]any) (*domains.CommandResult, error) {
	return b.Execute(ctx, &domains.CustomCommandRequest{SN: sn, TID: tid, CommandID: id, Params: params})
}

func (b *Base) GetCapabilities(_ context.Context, sn string) (*domains.CurrentCapabilities, error) {
	return b.Capabilities(sn), nil
}

func (b *Base) SendCustomCommand(ctx context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	return b.Execute(ctx, req)
}

func (b *Base) TakeOff(ctx context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
	return b.run(ctx, req.SN, req.TID, "flight.takeoff", coordinateParams(req.Coordinates))
}

func (b *Base) GoTo(ctx context.Context, req *domains.GoToRequest) (*domains.CommandResult, error) {
	return b.run(ctx, req.SN, req.TID, "navigation.go_to", coordinateParams(req.Coordinates))
}

func (b *Base) ReturnToHome(ctx context.Context, req *domains.ReturnToHomeRequest) (*domains.CommandResult, error) {
	p := map[string]any{}
	if req.Altitude != nil {
		p["altitude"] = float64(*req.Altitude)
	}
	return b.run(ctx, req.SN, req.TID, "flight.return_to_home", p)
}

func (b *Base) EnterManualControl(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "flight.manual.enter", nil)
}

func (b *Base) ExitManualControl(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "flight.manual.exit", nil)
}

func (b *Base) ManualControlInput(_ context.Context, input *domains.ManualControlInput) (*domains.CommandResult, error) {
	return domains.NotImplemented("manual control input is not implemented for this asset", input.SN), nil
}

func (b *Base) OpenCover(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "dock.open_cover", nil)
}

func (b *Base) CloseCover(ctx context.Context, sn string, force *bool) (*domains.CommandResult, error) {
	p := map[string]any{}
	if force != nil {
		p["force"] = *force
	}
	return b.run(ctx, sn, "", "dock.close_cover", p)
}

func (b *Base) StartCharging(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "dock.start_charging", nil)
}

func (b *Base) StopCharging(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "dock.stop_charging", nil)
}

func (b *Base) RebootAsset(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "asset.reboot", nil)
}

func (b *Base) BootUpSubAsset(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "asset.boot_sub_asset", map[string]any{"enabled": true})
}

func (b *Base) BootDownSubAsset(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "asset.boot_sub_asset", map[string]any{"enabled": false})
}

func (b *Base) LookAt(ctx context.Context, req *domains.LookAtRequest) (*domains.CommandResult, error) {
	p := coordinateParams(domains.Coordinates{Lat: req.Lat, Lon: req.Lon, Alt: float64(req.Alt)})
	if req.PayloadIndex != nil {
		p["payloadIndex"] = *req.PayloadIndex
	}
	if req.Locked != nil {
		p["locked"] = *req.Locked
	}
	return b.run(ctx, req.SN, req.TID, "gimbal.look_at", p)
}

func (b *Base) TakePhoto(ctx context.Context, req *domains.TakePhotoRequest) (*domains.CommandResult, error) {
	id := "camera.take_photo"
	if !b.Has(id) && b.Has("camera.capture_photo") {
		id = "camera.capture_photo"
	}
	return b.run(ctx, req.SN, req.TID, id, nil)
}

func (b *Base) ChangeLens(ctx context.Context, req *domains.ChangeLensRequest) (*domains.CommandResult, error) {
	p := map[string]any{}
	if req.Lens != nil {
		p["lens"] = *req.Lens
	}
	return b.run(ctx, req.SN, req.TID, "camera.change_lens", p)
}

func (b *Base) ChangeZoom(ctx context.Context, req *domains.ChangeZoomRequest) (*domains.CommandResult, error) {
	p := map[string]any{}
	if req.Lens != nil {
		p["lens"] = *req.Lens
	}
	if req.Zoom != nil {
		p["zoom"] = float64(*req.Zoom)
	}
	return b.run(ctx, req.SN, req.TID, "camera.change_zoom", p)
}

func (b *Base) EnableGimbalTracking(ctx context.Context, sn string, enabled bool) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "gimbal.tracking", map[string]any{"enabled": enabled})
}

func (b *Base) StartLiveStream(ctx context.Context, req *domains.LiveStreamStartRequest) (*domains.CommandResult, error) {
	return b.run(ctx, req.SN, req.TID, "stream.start", map[string]any{
		"videoId": req.VideoID, "streamServer": req.StreamServer, "videoType": req.VideoType})
}

func (b *Base) StopLiveStream(ctx context.Context, req *domains.LiveStreamStopRequest) (*domains.CommandResult, error) {
	return b.run(ctx, req.SN, req.TID, "stream.stop", map[string]any{"videoId": req.VideoID})
}

func (b *Base) LiveStreamSplitScreen(ctx context.Context, sn string, enabled bool) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "stream.split_screen", map[string]any{"enabled": enabled})
}

func (b *Base) EnterRemoteDebugMode(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "asset.remote_debug", map[string]any{"enabled": true})
}

func (b *Base) CloseRemoteDebugMode(ctx context.Context, sn string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "asset.remote_debug", map[string]any{"enabled": false})
}

func (b *Base) ChangeACMode(ctx context.Context, sn, mode string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, "", "asset.change_ac_mode", map[string]any{"mode": mode})
}

func (b *Base) GetDetections(context.Context, *domains.GetDetectionsRequest, func(*domains.DetectionResult) error) error {
	return nil
}

func (b *Base) StartTask(ctx context.Context, sn, taskID, tid string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, tid, "mission.start", map[string]any{"taskId": taskID})
}

func (b *Base) StopTask(ctx context.Context, sn, taskID, tid string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, tid, "mission.stop", map[string]any{"taskId": taskID})
}

func (b *Base) PrepareTask(ctx context.Context, sn, taskID, tid string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, tid, "mission.prepare", map[string]any{"taskId": taskID})
}

func (b *Base) PauseTask(ctx context.Context, sn, taskID, tid string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, tid, "mission.pause", map[string]any{"taskId": taskID})
}

func (b *Base) ResumeTask(ctx context.Context, sn, taskID, tid string) (*domains.CommandResult, error) {
	return b.run(ctx, sn, tid, "mission.resume", map[string]any{"taskId": taskID})
}

// coordinateParams leaves out a NaN component: NaN is the platform's "not provided".
func coordinateParams(c domains.Coordinates) map[string]any {
	p := map[string]any{}
	for key, v := range map[string]float64{"latitude": c.Lat, "longitude": c.Lon, "altitude": c.Alt} {
		if !math.IsNaN(v) {
			p[key] = v
		}
	}
	return p
}

var _ EdgeAdapter = (*Base)(nil)
