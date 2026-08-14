# zqnt-client-sdk-go

Go SDK for customer applications consuming the Zequent platform — the Go counterpart to
`client-java-sdk` / `client-python-sdk`.

## Status: covers the full new-model client surface

**Included:**
- `gen/` — generated protobuf/gRPC Go code for the full canonical protocol (`utils/zqnt-utils/src/main/proto/*.proto`).
- `connector/` — asset lookup, the full Skill Registry surface (`ObserveSkillContract`, `ListSkillContracts`,
  `SetSkillContractStatus`, `SetSkillContractPermissions`), scheduler CRUD (`GetScheduler`/`ListSchedulers`/
  `CreateScheduler(s)`/`UpdateScheduler`/`DeleteScheduler(s)`), operational policies (`GetActivePoliciesByType`,
  `GetAllActivePolicies`), and technical config (`GetTechnicalConfigs`) — mirroring client-java-sdk's `Connector`.
- `missionautonomy/` — Application CRUD + `ListApplications`, the full SkillExecution lifecycle
  (`CreateSimpleExecution`/`ExecuteSimple` for a single command, `CreateApplicationExecution`/`ExecuteApplication`
  for a named Skill out of a deployed Application, `GetSkillExecution`, `ListSkillExecutions`,
  `Start`/`Pause`/`Resume`/`CancelSkillExecution`, `SignalSkillExecution`), and `ResolveExecutionConfig` — mirroring
  client-java-sdk's `MissionAutonomy` (minus its dead, RPC-less deprecated Mission/Task methods — see below).
- `remotecontrol/` — the full client-facing command gateway (`RemoteControlService`): flight control
  (TakeOff/GoTo/ReturnToHome), manual control (Enter/ExitManualControl, a streaming RC-input channel, LookAt),
  dock ops (Open/CloseCover, Start/StopCharging), asset management (RebootAsset, BootSubAsset), debug/maintenance
  (SetRemoteDebugMode, ChangeAcMode), camera (ChangeLens/ChangeZoom, CapturePhoto), TTS, split-screen, detection
  control, runtime/capability discovery (GetAssetRuntime, GetCapabilities), and custom/integrator commands —
  mirroring client-java-sdk's `RemoteControl`. This is gRPC, not WebSocket — the WebSocket layer in the Java stack
  belongs to admin-console's own browser dashboard, not the SDK.
- `livedata/` — telemetry/detection/notification streaming (`StreamTelemetry`/`StreamDetections`/
  `StreamNotifications`, returning the raw gRPC server-streaming client — no SDK-managed auto-reconnect, see below)
  plus live-stream/camera/recording control (Start/StopLiveStream, ChangeLens/ChangeZoom, Start/StopRecording,
  CapturePhoto) — mirroring client-java-sdk's `LiveData`.

**Deliberately not included:**
- **Auto-reconnecting streams.** client-java-sdk's `LiveData.streamTelemetryData` manages reconnection with
  capped exponential backoff internally and hands back a `StreamHandle`. This package's `StreamTelemetry`/
  `StreamDetections`/`StreamNotifications` return the raw `grpc.ServerStreamingClient` instead — redialing on
  a broken stream is the caller's job. Worth adding if this SDK gets real usage.
- **The Java SDK's deprecated Mission/Task methods.** `MissionAutonomy.createMission`/`createTask`/etc. are kept
  in Java only for source compatibility, `@Deprecated`, and — as of the current `mission-autonomy.proto` — not
  backed by any RPC at all (`MissionAutonomyService` has no Mission/Task methods anymore). There was nothing
  working to mirror, so this package doesn't have them.
- A shared proto module: `gen/` is vendored directly into this repo rather than pulled from one common
  `zqnt-utils-go` — regenerate and copy by hand after a proto change (see `edge-go-sdk`'s README for the
  `protoc` invocation).
- `ApplicationExecutionSpecProto`'s Skill lookup is by (`applicationId`, `skillId`, optional `applicationVersion`)
  only — nothing here validates that combination against a deployed Application before sending the RPC; the
  server does that.

## Requirements

Go 1.24+. `go build ./...` / `go vet ./...` / `go test ./...` all pass as of this commit.
