# Migrating from 2.x to 3.0

Zequent 3.0 commands every asset the same way: ask the asset what it can do, then run a command by
its dotted id with a JSON object of params. The 2.x packages `remotecontrol` (one typed method per
command) and `livedata` are removed in 3.0; `control` and `telemetry` replace them.

```go
cfg, err := config.FromEnv()
conn, err := cfg.Dial(cfg.RemoteControl)
assets := control.New(conn)

set, err := assets.ListCapabilities(ctx, "DOCK-1")
result, err := assets.ExecuteCommand(ctx, "DRONE-1", "navigation.go_to",
	control.Params{"latitude": 52.52, "longitude": 13.405, "altitude": 60})
```

## Module and generated types

1. Module path: `github.com/Zequent/zqnt-client-sdk-go/v2` → `github.com/zequent/zqnt-client-sdk-go/v3`
   (lower-case owner, as for every other Zequent Go module).
2. Generated types: `github.com/Zequent/zqnt-client-sdk-go/v2/gen/...` →
   `github.com/zequent/zqnt-api-go/v3/gen/...`. One `sed` does both:
   ```
   grep -rl zqnt-client-sdk-go/v2 --include='*.go' . | xargs sed -i \
     -e 's#github.com/Zequent/zqnt-client-sdk-go/v2/gen/#github.com/zequent/zqnt-api-go/v3/gen/#g' \
     -e 's#github.com/Zequent/zqnt-client-sdk-go/v2/#github.com/zequent/zqnt-client-sdk-go/v3/#g'
   go get github.com/zequent/zqnt-client-sdk-go/v3 && go mod tidy
   ```

## Typed call → command id + params

Every row is `assets.ExecuteCommand(ctx, sn, "<command id>", control.Params{...})` (or
`ExecuteAndWait` with the same arguments). The capability's `input_schema` from `ListCapabilities`
is authoritative for an asset; the params below are the ones the built-in commands of every Zequent
adapter accept.

| 2.x call | Command id | Params |
|---|---|---|
| `remotecontrol.TakeOff(ctx, sn, coordinate)` | `flight.takeoff` | `latitude`, `longitude`, `altitude` |
| `remotecontrol.GoTo(ctx, sn, coordinate)` | `navigation.go_to` | `latitude`, `longitude`, `altitude` (metres above the **takeoff point**; left out = 40 m) |
| `remotecontrol.GoToWithOptions(..., GoToOptions{NoFlyZoneOverride: true})` | `navigation.go_to` | as above, plus the option `control.WithNoFlyZoneOverride()` |
| `remotecontrol.ReturnToHome(ctx, sn, altitude)` | `flight.return_to_home` | `altitude` (optional) |
| `remotecontrol.LookAt(ctx, sn, coordinate, locked)` | `gimbal.look_at` | `latitude`, `longitude`, `altitude`, `locked`, `payloadIndex` |
| `remotecontrol.EnterManualControl(...)` | `flight.manual.enter` | none |
| `remotecontrol.ExitManualControl(...)` | `flight.manual.exit` | none |
| `remotecontrol.OpenCover(ctx, sn)` | `dock.open_cover` | none |
| `remotecontrol.CloseCover(ctx, sn, force)` | `dock.close_cover` | `force` (boolean, optional) |
| `remotecontrol.StartCharging(ctx, sn)` | `dock.start_charging` | none |
| `remotecontrol.StopCharging(ctx, sn)` | `dock.stop_charging` | none |
| `remotecontrol.RebootAsset(ctx, sn)` | `asset.reboot` | none |
| `remotecontrol.BootSubAsset(ctx, sn, enabled)` | `asset.boot_sub_asset` | `enabled` (boolean: `true` boots, `false` shuts down) |
| `remotecontrol.SetRemoteDebugMode(ctx, sn, enabled)` | `asset.remote_debug` | `enabled` (boolean) |
| `remotecontrol.ChangeAcMode(ctx, req)` | `asset.change_ac_mode` | `mode` (one of the values in the capability's schema) |
| `remotecontrol.CapturePhoto(ctx, sn)` / `livedata.CapturePhoto(ctx, sn)` | `camera.take_photo` | none |
| `remotecontrol.LiveStreamSplitScreen(ctx, sn, enabled)` | `stream.split_screen` | `enabled` (boolean) |
| `remotecontrol.ControlDetection(ctx, req)` | `detection.start` / `detection.stop` (`detection.control` keeps the 2.x form) | the capability's schema |
| `remotecontrol.ChangeLens` / `livedata.ChangeLens` | `camera.change_lens` | `lens`, `videoId` |
| `remotecontrol.ChangeZoom` / `livedata.ChangeZoom` | `camera.change_zoom` | `lens`, `payloadIndex`, `zoom` |
| `livedata.StartLiveStream(ctx, req)` | `stream.start` | the capability's schema |
| `livedata.StopLiveStream(ctx, req)` | `stream.stop` | the capability's schema |
| `livedata.StartRecording(ctx, sn)` | `camera.start_recording` | none |
| `livedata.StopRecording(ctx, sn)` | `camera.stop_recording` | none |
| `remotecontrol.GetCapabilities(ctx, sn)` | `assets.ListCapabilities(ctx, sn)` | returns the v3 `CapabilitySet` |
| `remotecontrol.SendCustomCommand(ctx, sn, id, params, target)` | the capability's `command_id` | the same params; the target as `control.WithTarget(kind, ref)` |

Ids not in this table: `ListCapabilities` on the asset, or `public/v3/COMMAND_CATALOG.md` in
zqnt-protos.

**Leave a param out when you have no value.** The platform reads a missing coordinate as "not
given"; sending `0` means latitude 0 / longitude 0 / ground level.

`remotecontrol.ReportAssetRuntime` and `GetAssetRuntime` have no client counterpart: an asset's
online state is the `SourceStatus` of `telemetry.WatchTelemetry`, and the freshness of what it can
do is the `snapshot_state` of its `CapabilitySet`.

## Responses

| 2.x | 3.0 |
|---|---|
| `has_errors` / `error` on the response (an `error` from the typed call) | `*control.CommandError` with `State()`, `Category()`, `Retryable()` and the full `Result` |
| a refused call (gRPC status) | the same gRPC status: `status.Code(err)` |
| a command that takes time blocked on the typed RPC | `ACCEPTED`/`RUNNING` with a `command_execution_id`; wait with `ExecuteAndWait`, or `WatchCommand(ctx, id)` |
| `tid` | `result.GetCommandExecutionId()`: watch it, cancel it with `CancelCommand(ctx, id, reason)` |
| `result` of a custom command | `result.GetResult().AsMap()` |

## Manual control

| 2.x | 3.0 |
|---|---|
| `remotecontrol.OpenManualControlInputStream(ctx)` | `assets.StreamManualControl(ctx, sn)` |
| `stream.Send(*devicecontrol.ManualControlInputCommandRequest)` | `stick.Send(*edgev3.ManualControlInput)` (roll, pitch, yaw, throttle, optional gimbal pitch) |
| `stream.CloseAndRecv()` | `stick.Close()`: the number of inputs passed on to the asset |

Enter and leave manual control with the commands `flight.manual.enter` and `flight.manual.exit`.

## Telemetry, detections and notifications

| 2.x (`livedata`) | 3.0 (`telemetry`) |
|---|---|
| `StreamTelemetry(ctx, sn, frequencyMs, durationSeconds)` | `WatchTelemetry(ctx, sns, telemetry.WithMinInterval(...))` |
| `StreamDetections(ctx, sn)` | `WatchDetections(ctx, sns)` |
| `StreamNotifications(ctx, sn, eventTypes)` | `WatchAlerts(ctx, sns, telemetry.WithMinSeverity(...))` |
| per-vendor telemetry types | `TelemetrySample`: position, relative altitude, heading, speeds, battery; the rest in `details` under the capabilities' `telemetry_fields` |
| the raw gRPC stream | a `*telemetry.Watch` that skips heartbeats and returns `telemetry.ErrSilent` when the stream went quiet; watch again |

No serial watches every asset of the caller's organization; a dock's serial includes its drone.
