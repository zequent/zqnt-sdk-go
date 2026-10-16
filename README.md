# Zequent SDK for Go

One Go module, `github.com/zequent/zqnt-sdk-go/v3`, with two SDKs released together:

| Packages | Import | For |
|---|---|---|
| [`edge/...`](edge) | `github.com/zequent/zqnt-sdk-go/v3/edge` (package `edgesdk`), `.../edge/adapter`, ... | adapters that connect devices to the platform |
| [`client/...`](client) | `github.com/zequent/zqnt-sdk-go/v3/client/control`, `.../client/telemetry`, ... | applications that use the platform |

Both build on the public API module `github.com/zequent/zqnt-api/go/v3` ([zqnt-api](https://github.com/zequent/zqnt-api)).
One module rather than two: edge and client share one dependency set and one version, a consumer
that uses both resolves one `require` line, and the linker only includes the packages you import.

```bash
go get github.com/zequent/zqnt-sdk-go/v3@v3.0.0          # a release
go get github.com/zequent/zqnt-sdk-go/v3@pr-<n>-<sha>    # a PR preview
make check                                               # build, vet, test (make lint: golangci-lint v2)
```

## Edge SDK


Go SDK for connecting edge devices (drones, robots) to the Zequent platform via gRPC.

### Quick Start

**1. Add the SDK to your project:**

```bash
go env -w GONOSUMDB="github.com/Zequent/*,github.com/zequent/*"
go env -w GONOPROXY="github.com/Zequent/*,github.com/zequent/*"
go get github.com/zequent/zqnt-sdk-go/v3/edge@v3.0.0  # proto stubs come from github.com/zequent/zqnt-api/go/v3
```

**2. Implement your adapter:** embed `adapter.Base` and register each command once — id, input
and output JSON Schema, handler. The advertised capabilities, v3 `ExecuteCommand` and the v2 typed
RPCs all come from that registration.

```go
package main

import (
    "context"
    "net"

    edgesdk "github.com/zequent/zqnt-sdk-go/v3/edge"
    "github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
    "github.com/zequent/zqnt-sdk-go/v3/edge/adapter/domains"
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

See [`example/main.go`](edge/example/main.go) for a complete working example with graceful shutdown and logging.

---

### v3: talking to the platform

| What | v3 (tried first) | Fallback on `UNIMPLEMENTED` (older core) |
|---|---|---|
| Commands from the platform | `zqnt.edge.v3.EdgeAdapterService.ExecuteCommand` | v2 typed RPCs, served alongside |
| Command events (`LiveData().PublishCommandExecutionEvent`) | `EdgeGatewayService.PublishCommandEvent` under the platform's `command_execution_id`, `occurred_at` always set | v2 notification stream |
| Capabilities (on start, on every registry change, `client.RefreshCapabilities()`) | `EdgeGatewayService.ReportCapabilities`, incl. declared telemetry fields | v2 `ReportAssetRuntime` |
| `LiveData().PublishTelemetrySample` | `TelemetryIngestService.PublishTelemetry` | v2 `ProduceTelemetry`; catalog keys in `Details` land in their v2 fields, other keys are dropped |
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
  is `REJECTED` with error code `command.invalid_params` and a message naming each field. v3 params
  are validated as sent; on v2 the SDK first converts text the schema does not accept as text
  (`"13.405"` for a number, `"true"` for a boolean, `"pitch_down"` for the enum value `PITCH_DOWN`),
  exact conversions only (`schema.ConvertText`). Enum aliases (a DJI name such as `SDR_ONLY` or
  `IRONBOW1` for a standard value) are not converted: a 2.x platform must send the standard value.
- **Risk:** every capability carries a `CommandSafety`. A standard id gets the rating of the
  platform's command catalog (`domains.CatalogSafety`, e.g. `flight.takeoff` MOVE, `flight.land`
  CRITICAL); `adapter.WithSafety(...)` / `adapter.WithRisk(domains.CommandRiskCritical)` overrides
  it. A `vendor.*` command that moves or affects the asset must declare its own: an id outside the
  catalog declares none, and the platform then falls back to the 2.x prefix rule.
- **Pairing:** `client.EnsureAsset(ctx, asset)` binds to the asset with that serial or, when the
  platform does not know it, redeems the one-time claim code (`WithClaimCode`, default
  `ZQNT_CLAIM_CODE`) minted in the console; the claim decides the organization.
  `Connector().DescribeAssetClaim` names that organization without spending the code. An adapter
  cannot create an asset any other way: `Connector().RegisterAsset` was removed in 3.0.
- **Telemetry fields:** `DeclareTelemetryField` on the registry declares the keys of
  `TelemetrySample.Details`; they are published with the capabilities. Keys of the platform's
  catalog (`github.com/zequent/zqnt-api/go/v3/telemetry`, e.g. `dock.mode`, `wind.speed`)
  are described by `domains.StandardTelemetryField` and also reach a v2 platform.
- **Payloads:** `SetPayloads(sn, payloads)` on the registry sets the devices mounted on an asset
  (camera, speaker, spotlight, ...), published with the capabilities as a full snapshot: a payload
  left out is detached. A command acting on one is registered with
  `WithTarget(domains.CapabilityTargetPayload, payload.Ref)`.
- **Conformance kit:** `conformance.Run(t, adapter, conformance.Options{Events: recorder})` checks
  that every advertised id executes, every executable id is advertised, schemas parse and an
  `ACCEPTED` command is completed by an event with `occurred_at`. It executes commands — run it
  against fakes or a simulator. Publish events through `adapter.CommandEventPublisher` so the kit's
  `Recorder` can stand in for the client.

### Migrating from 2.x

Module path: `github.com/Zequent/zqnt-edge-sdk-go/v2` → `github.com/zequent/zqnt-sdk-go/v3/edge`
(lower-case owner, as for every other Zequent Go module). Generated types come from
`github.com/zequent/zqnt-api/go/v3/gen/...` (same package paths below `gen/`):

```bash
grep -rl zqnt-edge-sdk-go/v2 --include='*.go' . | xargs sed -i \
  's#github.com/Zequent/zqnt-edge-sdk-go/v2#github.com/zequent/zqnt-sdk-go/v3/edge#g'
go get github.com/zequent/zqnt-sdk-go/v3/edge && go mod tidy
```

### v2.0.0: the 2.0.0 wire contract

This line targets the platform's current contract — `zqnt-protos` `2.0.1` (the `2.0.0` contract
plus `simulator-control.proto`), the exact commit `utils/zqnt-utils` (Java) and therefore every
`core/` service builds from. The previous `v1.3.x` line deliberately froze on the older `1.3.1`
contract for customers still integrated against `edge-java-sdk` v1.3.0; that line is now behind
the running platform, not compatible with it.

**Proto source**: no local generation in this repo (no `buf.gen.yaml`, no `gen/`). Stubs and the
telemetry mapping come from the public module
[`github.com/zequent/zqnt-api/go/v3`](https://github.com/zequent/zqnt-api), the platform’s
customer contracts; the SDK depends on nothing else of the platform.

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
service depending on `github.com/zequent/zqnt-sdk-go/v3/edge` as a real tagged dependency, the same
way any other adapter built on this SDK would.

#### Local development

`go.mod` points at a published version of `github.com/zequent/zqnt-api/go/v3`, so a clone and CI resolve it
without any local setup. While the v3 line is still in review that's the PR preview build
(`pr-<n>-<sha>`, resolved as a pseudo-version); it becomes `v3.0.0` when that release is cut.

To build against the sibling monorepo checkout instead, use a **Go workspace** — never a committed
`replace`, which would break every consumer outside a full `zqnt-platform` tree:

```bash
go work init . ../zqnt-api/go   # git-ignored, along with go.work.sum
```

Keep that file free of comments: GoLand derives the module name from it and will build a package
path out of a leading comment block, failing with `malformed import path … invalid char ':'`.

`GOWORK=off` builds exactly as CI does. Delete `go.work` when you're done.

### How It Works

Your application acts as a gRPC server that the Zequent backend connects to. You implement `EdgeAdapter` — the interface that receives commands (TakeOff, GoTo, ReturnToHome, etc.) and translates them to your hardware.

```
Zequent Backend  ──gRPC──>  Your App (EdgeAdapter)  ──>  Hardware
```

The SDK manages the gRPC server, connection lifecycle, telemetry streaming, and reconnection automatically.

---

### Available Commands

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
| `SendCustomCommand` | 2.x only: runs a registered command; an unregistered id goes to `LegacyCustomCommand` (refused unless the adapter overrides it). v3 `ExecuteCommand` runs registered commands only |

---

### Configuration Options

```go
client, err := edgesdk.NewEdgeClient(
    backendAddr,
    deviceSN,
    &MyDroneAdapter{},
    edgesdk.WithLogger(myLogger),   // custom slog.Logger
)
```

#### Authentication

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

### Troubleshooting

**`verifying module: 404 Not Found`**
- Run `go env -w GONOSUMDB="github.com/Zequent/*"` and `go env -w GONOPROXY="github.com/Zequent/*"`

**`fatal: could not read Username`**
- Make sure you have access to the repository and are authenticated with GitHub


## Client SDK


Go SDK for customer applications consuming the Zequent platform — the Go counterpart to
`client-java-sdk` / `client-python-sdk`.

```
go get github.com/zequent/zqnt-sdk-go/v3/client
```

### Commanding assets (3.x): `control`

On a 3.x platform every action on an asset is a **capability** with a dotted command id
(`flight.takeoff`, `navigation.go_to`, `dock.open_cover`, `vendor.<vendor>.<name>`) and JSON Schemas
for its parameters and result. There is one way to run any of them — no per-command method — so a
command a new adapter adds works without a new SDK release.

```go
cfg, err := config.FromEnv()
conn, err := cfg.Dial(cfg.RemoteControl)
assets := control.New(conn)

set, err := assets.ListCapabilities(ctx, "DRONE-1") // ids, schemas, risk, completion mode

result, err := assets.ExecuteCommand(ctx, "DOCK-1", "dock.open_cover", nil)

result, err = assets.ExecuteAndWait(ctx, "DRONE-1", "navigation.go_to",
	control.Params{"latitude": 52.52, "longitude": 13.405, "altitude": 60},
	control.WithReason("site inspection"))
```

- `ListCapabilities(ctx, sn, ...)` — what the asset can do right now. When its adapter is not
  reachable the platform answers with the last snapshot, marked `SNAPSHOT_STATE_STALE`.
- `ExecuteCommand(ctx, sn, commandID, params, ...)` — params are a `control.Params`
  (`map[string]any`, sent as a `google.protobuf.Struct`). A command that finishes with the reply
  returns `SUCCEEDED` and its result; one that takes time (takeoff, go-to) returns `ACCEPTED` or
  `RUNNING` with a `command_execution_id`, and its outcome follows as events.
- `ExecuteAndWait(...)` — `ExecuteCommand` plus waiting for that run's final event (ctx bounds it).
- `WatchCommand(ctx, commandExecutionID)` / `WatchAsset(ctx, sn)` — command events from now on; a
  single run's stream ends with `io.EOF` after its final event.
- `CancelCommand(ctx, commandExecutionID, reason)`.
- Options: `WithTarget` (a payload, sub-asset or component), `WithTimeout`, `WithReason` (required
  for CRITICAL commands), `WithNoFlyZoneOverride` (organization or system admin only),
  `WithIdempotencyKey`, `WithRequestID`.

Errors: a command the platform refused (`REJECTED`: unknown id, parameters, precondition,
permission) or that did not succeed (`FAILED`, `CANCELLED`, `TIMED_OUT`) is a `*control.CommandError`
with `State()`, `Category()`, `Retryable()` and the full `Result`. A call that never got that far
keeps its gRPC status — `status.Code(err)` is `Unauthenticated`, `PermissionDenied`,
`InvalidArgument`, `Unavailable`, ...

`control` is a client of `zqnt.control.v3.RemoteControlService`, served by remote-control
(`REMOTE_CONTROL_SERVICE_*`). The message types are those of the public
`zqnt-api` Go module (`github.com/zequent/zqnt-api/go/v3/gen/zqnt/...`), the same Go types the edge
SDK, the simulator and the Integration Hub use.

`examples/command` lists an asset's capabilities and runs one:
`go run ./examples/command DRONE-1 flight.takeoff '{"altitude": 40}'`.

#### Manual control

Stick input goes over one stream per asset. Put the asset in manual control first, then send
inputs (roll, pitch, yaw, throttle from -1 to 1) at your control rate:

```go
if _, err := assets.ExecuteAndWait(ctx, "DRONE-1", "flight.manual.enter", nil); err != nil { ... }
stick, err := assets.StreamManualControl(ctx, "DRONE-1")
err = stick.Send(&edgev3.ManualControlInput{Pitch: 0.2, Throttle: 0.5})
err = stick.Send(&edgev3.ManualControlInput{Throttle: 0.5, GimbalPitch: proto.Float32(-30)})
accepted, err := stick.Close()
```

The first input names the asset; one stream controls one asset. `Close` ends the session and
returns the number of inputs the platform passed on to the asset (an error in its answer is a
`*control.CommandError`); cancelling `ctx` aborts the stream. When the platform refuses the session
(no adapter reachable, an asset of another organization) or the adapter drops it, `Send` and `Close`
return that error with its gRPC status; `Send` after `Close` is `control.ErrManualControlClosed`.
`edgev3` is `github.com/zequent/zqnt-api/go/v3/gen/zqnt/edge/v3`.

### Telemetry, detections and alerts (3.x): `telemetry`

```go
conn, err := cfg.Dial(cfg.LiveData)
watch, err := telemetry.New(conn).WatchTelemetry(ctx, []string{"DOCK-1"}, telemetry.WithMinInterval(time.Second))
defer watch.Close()
for {
	item, err := watch.Recv()
	if err != nil { ... } // io.EOF, telemetry.ErrSilent or the gRPC error
	if sample := item.GetSample(); sample != nil {
		fmt.Println(sample.GetAsset().GetSn(), sample.GetPosition(), sample.GetBatteryPercent(), sample.GetDetails().AsMap())
	} else {
		fmt.Println(item.GetSourceStatus().GetAsset().GetSn(), item.GetSourceStatus().GetState())
	}
}
```

`WatchTelemetry` is a client of `zqnt.telemetry.v3.TelemetryService` on live-data
(`LIVE_DATA_SERVICE_*`). A `TelemetrySample` has the shared fields (position, relative altitude,
heading, speeds, battery) and the device-specific `details` under the keys of the capabilities'
`telemetry_fields`. No asset watches every asset of the caller's organization; a dock's serial
includes its drone. `WithMinInterval` keeps at most one sample per asset per interval. A
`SourceStatus` comes first for every watched asset, then on each change to online, stale or no
data; heartbeats are skipped. When neither an item nor a heartbeat arrives within
`DefaultHeartbeatTimeout` (35 s, `WithHeartbeatTimeout`), `Recv` returns `telemetry.ErrSilent`. A
watch does not reconnect: watch again.

Detections and alerts are watched the same way, with the same heartbeat timeout:

```go
detections, err := telemetry.New(conn).WatchDetections(ctx, []string{"DRONE-1"})
batch, err := detections.Recv() // batch.GetBatch(): asset, observed_at, stream_url, detections

alerts, err := telemetry.New(conn).WatchAlerts(ctx, nil,
	telemetry.WithMinSeverity(telemetryv3.AlertSeverity_ALERT_SEVERITY_WARNING))
alert, err := alerts.Recv() // alert.GetAlert(): asset, severity, a stable code such as "dock.rain", message, details
```

### Live video streams (3.x): `stream`

```go
conn, err := cfg.Dial(cfg.LiveData)
streams, err := stream.New(conn).ListLiveStreams(ctx)
for _, s := range streams {
	fmt.Println(s.GetAsset().GetSn(), s.GetPath(), s.GetSince().AsTime(), s.GetViewers())
}
```

`ListLiveStreams` returns every stream of the caller's organization that is live right now: the
asset publishing it (for a docked aircraft, the aircraft), the media server's path, since when it
is received and how many sessions read it. It is a client of `zqnt.stream.v3.LiveStreamService` on
live-data (`LIVE_DATA_SERVICE_*`). The error keeps its gRPC status: `PermissionDenied` when the
organization's license does not include live streams, `Unavailable` when the platform has no
current answer from the media server.

### Packages

- `control/` — the 3.x command API and manual control above.
- `telemetry/` — 3.x telemetry samples and source status, detections and alerts.
- `stream/` — 3.x list of the live video streams.
- `connector/` — asset lookup, the Skill Registry, scheduler CRUD, operational policies and
  technical config (2.x contract, still served).
- `missionautonomy/` — Applications and the SkillExecution lifecycle (2.x contract, still served).
- `auth/`, `config/` — the client credential and where the platform is, shared by every package.

Generated types come from
[zqnt-api](https://github.com/zequent/zqnt-api) (`go/`): `gen/<service>/proto` for the
2.x contracts, `gen/zqnt/<domain>/v3` for 3.x. This repository has no generated code of its own.

### Migrating from 2.x

3.0 offers only the command API: the 2.x packages `remotecontrol` (typed commands) and `livedata`
are gone. [MIGRATION.md](client/MIGRATION.md) maps every 2.x call to its command id or `telemetry` watch.

### Authentication

The platform refuses every call that carries no credential. An organization administrator issues a
**client credential** in the console under **Deploy → Access & Integrations → Credentials** (kind
**client**). It is shown once, belongs to that one organization, and reaches only that
organization's assets, Applications and runs — never users, organizations or other administration.

The clients here wrap a connection you dial, so the credential is a set of dial options:

```go
opts := append(auth.DialOptions(""), // "" reads ZQNT_CLIENT_TOKEN; or pass the token itself
	grpc.WithTransportCredentials(insecure.NewCredentials()))
conn, err := grpc.NewClient("core.example.com:8010", opts...)
assets := connector.New(conn) // or control.New(conn) when dialing remote-control
```

It is sent as `authorization: Bearer <token>` on every call, unary and streaming. A refusal keeps its
gRPC code (`status.Code(err)`) with a message that says what to do: `Unauthenticated` (no credential,
or an expired/revoked one) or `PermissionDenied` (an asset of another organization, or an
administrative call). `auth.Credentials` is the same token as a `grpc.WithPerRPCCredentials` value
for code that prefers that (wrap its errors with `auth.Explain`).

#### Local development and deployment: `config`

`config.FromEnv()` reads the variables every Zequent client SDK (Java, Python, Go) reads, so one
`.env` works for every language; nothing set is the local development stack (`quarkus:dev` or
`docker-compose.local.yml`):

| Variable | Local default (nothing set) |
|---|---|
| `CONNECTOR_SERVICE_HOST` / `_PORT` / `_USE_PLAINTEXT` | `localhost` / `8010` / `true` |
| `REMOTE_CONTROL_SERVICE_HOST` / `_PORT` / `_USE_PLAINTEXT` | `localhost` / `8002` / `true` |
| `LIVE_DATA_SERVICE_HOST` / `_PORT` / `_USE_PLAINTEXT` | `localhost` / `8003` / `true` |
| `MISSION_AUTONOMY_SERVICE_HOST` / `_PORT` / `_USE_PLAINTEXT` | `localhost` / `8004` / `true` |
| `ZQNT_CLIENT_TOKEN` | none — issue one in your local console too |

```go
cfg, err := config.FromEnv()
conn, err := cfg.Dial(cfg.Connector) // TLS unless _USE_PLAINTEXT, plus the client credential
assets := connector.New(conn)
```

A deployment sets the hosts, `_USE_PLAINTEXT=false` for TLS whenever traffic leaves a private
network, and `ZQNT_CLIENT_TOKEN` from its secret store — never from a committed file. There is
deliberately no built-in development credential. Printing a `config.Config` never prints the token.

#### A credential that is not one fixed token

A service that forwards its own caller's token sets it per call — `auth.WithToken(ctx, token)` — or
registers its own interceptors ahead of the SDK's (`cfg.Dial(endpoint, grpc.WithChainUnaryInterceptor(...))`,
or before `auth.DialOptions` when dialing yourself; grpc-go runs chained interceptors in the order
given). An `authorization` header already on the call wins and the connection's token is then not sent.

### Requirements

Go 1.26+. `go build ./...`, `go vet ./...` and `go test ./...` pass (with a parent `go.work`, run
them with `GOWORK=off`).


## Moving from the old repositories

This repository replaces `zequent/zqnt-edge-sdk-go` and `zequent/zqnt-client-sdk-go` from 3.0 on; their
history is kept here under `edge/` and `client/`. The 2.x line stays in the old repositories.

| Before | Now |
|---|---|
| `github.com/zequent/zqnt-edge-sdk-go/v3` | `github.com/zequent/zqnt-sdk-go/v3/edge` |
| `github.com/zequent/zqnt-edge-sdk-go/v3/<pkg>` | `github.com/zequent/zqnt-sdk-go/v3/edge/<pkg>` |
| `github.com/zequent/zqnt-client-sdk-go/v3/<pkg>` | `github.com/zequent/zqnt-sdk-go/v3/client/<pkg>` |
| `github.com/zequent/zqnt-api-go/v3/...` | `github.com/zequent/zqnt-api/go/v3/...` |

```bash
grep -rlE 'zqnt-(edge|client)-sdk-go/v3|zqnt-api-go/v3' --include='*.go' --include=go.mod . | xargs sed -i \
  -e 's#github.com/zequent/zqnt-edge-sdk-go/v3#github.com/zequent/zqnt-sdk-go/v3/edge#g' \
  -e 's#github.com/zequent/zqnt-client-sdk-go/v3#github.com/zequent/zqnt-sdk-go/v3/client#g' \
  -e 's#github.com/zequent/zqnt-api-go/v3#github.com/zequent/zqnt-api/go/v3#g'
go get github.com/zequent/zqnt-sdk-go/v3@<tag> && go mod tidy
```

Drop the old `require` lines: the old and the new API module in one binary register the same protos twice.
Moving from the 2.x typed calls to commands: [client/MIGRATION.md](client/MIGRATION.md).

## License

Licensed under the [Apache License, Version 2.0](LICENSE); see [NOTICE](NOTICE).
