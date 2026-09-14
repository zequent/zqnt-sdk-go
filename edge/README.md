# zqnt-edge-sdk-go

Go SDK for connecting edge devices (drones, robots) to the Zequent platform via gRPC.

## Quick Start

**1. Add the SDK to your project:**

```bash
go env -w GONOSUMDB="github.com/Zequent/*,github.com/zequent/*"
go env -w GONOPROXY="github.com/Zequent/*,github.com/zequent/*"
go get github.com/Zequent/zqnt-edge-sdk-go/v2@v2.0.0  # proto stubs come from github.com/zequent/zqnt-utils-golang/v2@v2.0.0
```

**2. Implement your adapter:**

```go
package main

import (
    "context"
    "net"

    edgesdk "github.com/Zequent/zqnt-edge-sdk-go/v2"
    "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
    "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
)

// Embed UnimplementedEdgeAdapter — only override the commands your hardware supports.
// All other commands automatically return NOT_IMPLEMENTED.
type MyDroneAdapter struct {
    adapter.UnimplementedEdgeAdapter
}

func (a *MyDroneAdapter) TakeOff(ctx context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
    // send takeoff command to your hardware here
    return domains.SuccessWithTID("ok", req.TID, req.SN), nil
}

func main() {
    client, _ := edgesdk.NewEdgeClient(
        "your-backend:50051", // Zequent backend address
        "YOUR-DEVICE-SN",     // device serial number
        &MyDroneAdapter{},
    )

    lis, _ := net.Listen("tcp", ":9090")
    client.StartServing(context.Background(), lis)
}
```

**3. Run it:**

```bash
BACKEND_ADDR=your-backend:50051 DEVICE_SN=YOUR-SN go run main.go
```

See [`example/main.go`](example/main.go) for a complete working example with graceful shutdown and logging.

---

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

**Testing**: `adapter/grpc` and `discovery` have unit tests; the rest of the SDK is verified by
`go build`/`go vet` plus live-verifying the simulator built on it against a real running
connector/live-data/remote-control/mission-autonomy stack.

**The simulator built on this SDK lives in its own repo**,
[`github.com/Zequent/zqnt-simulator`](https://github.com/Zequent/zqnt-simulator) — a standalone
service depending on `github.com/Zequent/zqnt-edge-sdk-go/v2` as a real tagged dependency, the same
way any other adapter built on this SDK would.

### Local development

`go.mod` carries a `replace` pointing `zqnt-utils-golang/v2` at the sibling monorepo checkout while
the two move together. Drop it once the dependency's own tag is published, so a consumer resolves
the real module.

---

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

---

## Troubleshooting

**`verifying module: 404 Not Found`**
- Run `go env -w GONOSUMDB="github.com/Zequent/*"` and `go env -w GONOPROXY="github.com/Zequent/*"`

**`fatal: could not read Username`**
- Make sure you have access to the repository and are authenticated with GitHub
