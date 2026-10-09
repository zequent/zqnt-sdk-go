# zqnt-edge-sdk-go

Go SDK for connecting edge devices (drones, robots) to the Zequent platform via gRPC.

## Quick Start

**1. Add the SDK to your project:**

```bash
go env -w GONOSUMDB="github.com/Zequent/*,github.com/zequent/*"
go env -w GONOPROXY="github.com/Zequent/*,github.com/zequent/*"
go get github.com/Zequent/zqnt-edge-sdk-go/v2@v2.0.0  # proto stubs come from github.com/zequent/zqnt-utils-golang/v2@v2.0.0
```

**2. Implement your adapter:** embed `adapter.Base` and register each command once — id, input
and output JSON Schema, handler. The advertised capabilities, v3 `ExecuteCommand` and the v2 typed
RPCs all come from that registration.

```go
package main

import (
    "context"
    "net"

    edgesdk "github.com/Zequent/zqnt-edge-sdk-go/v2"
    "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
    "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
)

type MyDrone struct {
    adapter.Base
}

func (d *MyDrone) takeOff(ctx context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
    altitude := req.Params["altitude"].(float64) // validated against the input schema
    // send takeoff to your hardware; report completion under req.CommandExecutionID
    return domains.Success("climbing", req.SN), nil
}

func main() {
    d := &MyDrone{}
    d.MustRegisterCommand("flight.takeoff",
        map[string]any{"type": "object", "required": []any{"altitude"},
            "properties": map[string]any{"altitude": map[string]any{"type": "number"}}},
        nil, d.takeOff,
        adapter.WithCompletion(domains.CompletionAsynchronous, "flight.takeoff.completed"))

    client, _ := edgesdk.NewEdgeClient("your-backend:50051", "YOUR-DEVICE-SN", d)
    lis, _ := net.Listen("tcp", ":9090")
    client.StartServing(context.Background(), lis)
}
```

Adapters written against the typed methods (`adapter.UnimplementedEdgeAdapter` + `TakeOff`, …)
keep working; those methods are deprecated in favour of the registry.

**3. Run it:**

```bash
BACKEND_ADDR=your-backend:50051 DEVICE_SN=YOUR-SN go run main.go
```

See [`example/main.go`](example/main.go) for a complete working example with graceful shutdown and logging.

---

## v3: talking to the platform

| What | v3 (tried first) | Fallback on `UNIMPLEMENTED` (older core) |
|---|---|---|
| Commands from the platform | `zqnt.edge.v3.EdgeAdapterService.ExecuteCommand` | v2 typed RPCs, served alongside |
| Command events (`LiveData().PublishCommandExecutionEvent`) | `EdgeGatewayService.PublishCommandEvent` under the platform's `command_execution_id`, `occurred_at` always set | v2 notification stream |
| Capabilities (on start, on every registry change, `client.RefreshCapabilities()`) | `EdgeGatewayService.ReportCapabilities`, incl. declared telemetry fields | v2 `ReportAssetRuntime` |
| `LiveData().PublishTelemetrySample` | `TelemetryIngestService.PublishTelemetry` | v2 `ProduceTelemetry` with the shared fields; `Details` are dropped |
| `LiveData().PublishDetections` | `TelemetryIngestService.PublishDetections` | v2 `ProduceDetection` |
| `LiveData().PublishAlert` | `TelemetryIngestService.PublishAlerts` | none: dropped |

After `UNIMPLEMENTED` the SDK stays on v2 for 10 minutes, then tries v3 again. The v3 streams are
long-lived and reconnect with backoff; an item sent while a stream waits for its next attempt gets
`livedata.ErrReconnecting`, and the first item sent to a v2-only platform can be lost before the
SDK switches. remote-control serves `EdgeGatewayService`: point the SDK at it with
`WithRemoteControlAddr` unless the main endpoint multiplexes it. The v2 telemetry API
(`ProduceTelemetryData`, `ProduceTelemetry`) is unchanged.

- **Params** are validated against the command's input schema before the handler runs; whole
  numbers under an `integer` property arrive as `int64`, every other number as `float64`. A mismatch
  is `REJECTED` with error code `command.invalid_params` and a message naming each field.
- **Telemetry fields:** `DeclareTelemetryField` on the registry declares the keys of
  `TelemetrySample.Details`; they are published with the capabilities.
- **Conformance kit:** `conformance.Run(t, adapter, conformance.Options{Events: recorder})` checks
  that every advertised id executes, every executable id is advertised, schemas parse and an
  `ACCEPTED` command is completed by an event with `occurred_at`. It executes commands — run it
  against fakes or a simulator. Publish events through `adapter.CommandEventPublisher` so the kit's
  `Recorder` can stand in for the client.

## v2.0.0: the 2.0.0 wire contract

This line targets the platform's current contract — `zqnt-protos` `2.0.1` (the `2.0.0` contract
plus `simulator-control.proto`), the exact commit `utils/zqnt-utils` (Java) and therefore every
`core/` service builds from. The previous `v1.3.x` line deliberately froze on the older `1.3.1`
contract for customers still integrated against `edge-java-sdk` v1.3.0; that line is now behind
the running platform, not compatible with it.

Per Go's semantic import versioning the module path carries the major version from here on:
`github.com/Zequent/zqnt-edge-sdk-go/v2`.

**Proto source**: no local generation in this repo (no `buf.gen.yaml`, no `gen/`). Stubs come from
[`github.com/zequent/zqnt-utils-golang/v2`](../../../utils/zqnt-utils-golang) `v2.0.0` — a
published, versioned dependency, mirroring how `edge-java-sdk` depends on `zqnt-utils-java` and
`edge-python-sdk` on the `zqnt-utils` pip package, rather than generating proto code inline.

**What actually moved, and what didn't.** `edge.proto`, `common.proto`, `live-data.proto`,
`live-data-types.proto`, `remote-control.proto` and `simulator-control.proto` are byte-identical
between `1.3.1` and `2.0.1` — an edge adapter's whole command and telemetry surface did not move,
so an adapter written against v1.3.x needs no behavioural changes, only the import-path bump. The
breaking changes are confined to `connector.proto`/`mission-autonomy*.proto`: Mission/Task CRUD and
the Task lifecycle RPCs are gone, replaced by Applications, Skills and the `SkillExecution`
lifecycle. Every RPC this SDK calls survived that rewrite.

**`SchedulerDTO` is mission-free** (`missionautonomy`): `MissionID`/`TaskID` are gone — a schedule
now names its target directly through `AssetSN` plus either `CommandID` or
`ApplicationID`+`SkillID`, with `ExecutionParameters` and `AutoStart`.

**`domains.Capability` carries the full 2.0.0 command contract**: `InputSchema`/`OutputSchema`
(JSON Schema), `Errors`, `Events`, `Requirements`, `SkillID`, `Source`, `Provider`, alongside the
existing `TargetType`/`SchemaVersion`. All optional — a capability that sets none produces exactly
the wire message a pre-2.0.0 adapter did. They are worth setting: admin-console's
`SkillCatalogService` harvests precisely these off `GetCapabilities` into the persisted Skill
Registry, and derives a console form's `ui:schema` from `InputSchema`, so a command advertised
without one can only be called by guessing its params.

**`LiveDataService` publishes command-execution events** (`livedata`):
`PublishCommandExecutionEvent` reports one stage of a physical command's lifecycle — accepted,
running with progress, succeeded, failed, cancelled — over `ProduceNotification`. This is not
optional bookkeeping for an adapter that runs capability executions. mission-autonomy's
`EdgeExecutionNodeDispatcher` dispatches the typed flight commands (`flight.takeoff`,
`navigation.go_to`, `gimbal.look_at`, `flight.return_to_home`) and *every* custom command
asynchronously: the skill node goes to ACCEPTED and waits for an event carrying the execution id
the platform sent, which arrives at the adapter as the request's `TID`
(`capexec:<executionId>:<nodeId>`). An adapter whose RPC answers success but which publishes no
event leaves its node running until the whole execution times out. `edge-python-sdk`'s
`NotificationPublisher` has done this since 1.3.x — see `adapters/mavlink-adapter`'s arrival
watchers for the reference shape — and this is the Go equivalent.

`SN`, `ExternalExecutionID` and `OccurredAt` are all required on the wire: live-data's
`CommandExecutionEventPublisher` drops an event missing any of them, and because notifications are
streamed fire-and-forget the publish still succeeds, so the adapter sees nothing. The SDK validates
the first two and defaults the third rather than letting that happen.

Notifications share one stream per process, unlike telemetry's per-SN streams: they are occasional,
already name their asset in the event body, and one stream keeps a command's RUNNING and SUCCEEDED
in order.

**Redis discovery keys carry the `zqnt:` prefix again** (`discovery`): `CacheKeys` has it on the
2.0.0 line and didn't on 1.3.x, where this package correctly dropped it. The keys must match the
platform line being deployed against exactly — with the wrong ones an adapter registers with
Connector and streams telemetry perfectly happily while every command comes back
`Asset not connected: <sn>`, because `GrpcEndpointRouter`'s sn → vendor → endpoint lookup reads
keys nothing wrote to. Verified against a running 2.0.0 stack.

**Task-lifecycle commands carry the asset sn** (`StartTask`/`StopTask`/`PrepareTask`/
`PauseTask`/`ResumeTask` — breaking): one adapter process serves a whole vendor's fleet behind one
endpoint, so without the sn a multi-device adapter can't tell which device a `StopTask` is for.
`TaskCommandRequest.base.sn` always carried it; it just wasn't passed through. `StopTask` is the
one that matters on this contract — `EdgeExecutionNodeDispatcher#cancel` uses it to physically
cancel a running command.

**Not implemented here**: `StartRecording`/`StopRecording`/`RegisterAsset`/`DeregisterAsset` have
wire RPCs but no `adapter.EdgeAdapter` method, so they inherit a clean `codes.Unimplemented` —
the SDK's standing convention ("only the commands a device supports need to be overridden").
`skillregistry/` (Connector's `ObserveSkillContract`/`ListSkillContracts`) is deliberately absent:
the platform populates that registry itself from the capability snapshots it already collects, so
an adapter has nothing to publish into it.

**Testing**: `adapter/grpc`, `discovery` and `livedata` have unit tests; the rest of the SDK is verified by
`go build`/`go vet` plus live-verifying the simulator built on it against a real running
connector/live-data/remote-control/mission-autonomy stack.

**The simulator built on this SDK lives in its own repo**,
[`github.com/Zequent/zqnt-simulator`](https://github.com/Zequent/zqnt-simulator) — a standalone
service depending on `github.com/Zequent/zqnt-edge-sdk-go/v2` as a real tagged dependency, the same
way any other adapter built on this SDK would.

### Local development

`go.mod` points at a published version of `zqnt-utils-golang/v2`, so a clone and CI resolve it
without any local setup. While the v2 line is still in review that's the PR preview build
(`pr-<n>-<sha>`, resolved as a pseudo-version); it becomes `v2.0.0` when that release is cut.

To build against the sibling monorepo checkout instead, use a **Go workspace** — never a committed
`replace`, which would break every consumer outside a full `zqnt-platform` tree:

```bash
cp go.work.example go.work   # git-ignored, along with go.work.sum
```

Keep that file free of comments: GoLand derives the module name from it and will build a package
path out of a leading comment block, failing with `malformed import path … invalid char ':'`.

`GOWORK=off` builds exactly as CI does. Delete `go.work` when you're done.

## How It Works

Your application acts as a gRPC server that the Zequent backend connects to. You implement `EdgeAdapter` — the interface that receives commands (TakeOff, GoTo, ReturnToHome, etc.) and translates them to your hardware.

```
Zequent Backend  ──gRPC──>  Your App (EdgeAdapter)  ──>  Hardware
```

The SDK manages the gRPC server, connection lifecycle, telemetry streaming, and reconnection automatically.

---

## Available Commands

Override any of these methods in your adapter:

| Method | Description |
|--------|-------------|
| `TakeOff` | Take off to a given altitude |
| `ReturnToHome` | Return to home position |
| `GoTo` | Fly to coordinates |
| `EnterManualControl` / `ExitManualControl` | Manual RC control mode |
| `ManualControlInput` | Streaming manual control inputs |
| `LookAt` | Point gimbal at coordinates |
| `TakePhoto` | Capture a photo |
| `EnableGimbalTracking` | Enable object tracking |
| `GetDetections` | Stream object detection results |
| `GetCapabilities` | Report device capabilities |
| `PauseTask` / `ResumeTask` | Pause/resume a running task |
| `LiveStreamSplitScreen` | Toggle livestream split-screen view |
| `SendCustomCommand` | Vendor/adapter-specific escape hatch for commands with no dedicated RPC |

---

## Configuration Options

```go
client, err := edgesdk.NewEdgeClient(
    backendAddr,
    deviceSN,
    &MyDroneAdapter{},
    edgesdk.WithLogger(myLogger),   // custom slog.Logger
)
```

### Authentication

Both directions are authenticated (package `auth`):

- `ZQNT_EDGE_TOKEN` / `WithEdgeToken` -- the adapter's edge credential, sent on every call into the
  platform. Issue one in the console (Edge Credentials, `POST /api/admin-console/edge-credentials`)
  or with `core/scripts/mint-edge-credential.py`. The platform refuses calls without it.
- `ZQNT_PLATFORM_PUBLIC_KEY` (alias `SERVICE_AUTH_PUBLIC_KEY`) / `WithPlatformPublicKey` -- the
  platform's service public key. The adapter's server refuses every command not signed with it;
  without the key it refuses everything.
- `ZQNT_EDGE_AUTH_DISABLED=true` / `WithoutPlatformAuth()` -- accept unauthenticated commands. Local
  SITL/simulators only.

---

## Troubleshooting

**`verifying module: 404 Not Found`**
- Run `go env -w GONOSUMDB="github.com/Zequent/*"` and `go env -w GONOPROXY="github.com/Zequent/*"`

**`fatal: could not read Username`**
- Make sure you have access to the repository and are authenticated with GitHub
