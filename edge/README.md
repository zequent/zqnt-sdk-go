# zqnt-edge-sdk-go

Go SDK for connecting edge devices (drones, robots) to the Zequent platform via gRPC.

## Quick Start

**1. Add the SDK to your project:**

```bash
go env -w GONOSUMDB="github.com/Zequent/*"
go env -w GONOPROXY="github.com/Zequent/*"
go get github.com/Zequent/zqnt-edge-sdk-go@latest
```

**2. Implement your adapter:**

```go
package main

import (
    "context"
    "net"

    edgesdk "github.com/Zequent/zqnt-edge-sdk-go"
    "github.com/Zequent/zqnt-edge-sdk-go/adapter"
    "github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
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

## Skill Registry (new)

`skillregistry` lets an adapter self-report its own command contracts directly into the platform's
persisted Skill Registry, alongside the older `GetCapabilities` snapshot below:

```go
import (
    "github.com/Zequent/zqnt-edge-sdk-go/skillregistry"
    connectorpb "github.com/Zequent/zqnt-edge-sdk-go/gen/connector/proto"
)

svc := skillregistry.NewServiceImpl(connectorpb.NewConnectorServiceClient(conn), logger)
svc.ObserveSkillContract(ctx, &connectorpb.SkillContractProtoDTO{CommandId: "acme.custom_scan"})
```

It's generated from the current proto schema (`gen/connector/proto`) — as of 2026-09-02, that's now
the *only* schema this SDK generates from; see "One proto source of truth" below.

### One proto source of truth (as of 2026-09-02)

Until 2026-09-02 this SDK generated from two independent schemas: a `proto/` git submodule
(`zqnt-protos`, a separately-versioned repo) that every RPC in `adapter/`, `connector/`, `livedata/`,
and `missionautonomy/` was built against, and a second, up-to-date generation vendored alongside it
for `skillregistry` alone (see git history for that reasoning). The submodule had drifted badly —
frozen months before the platform's Skill/Capability/Application/mission-autonomy migration, so its
`connector.proto`/`edge.proto`/`mission-autonomy.proto` had no `ObserveSkillContract`, no
`CapabilityState`, no Application/SkillExecution API at all, and `connector`/`missionautonomy`'s
whole Mission/Task/Scheduler CRUD surface no longer exists on the real backend.

The submodule and its generated `gen/proto` package are gone. Every package in this SDK now
generates from `../../../utils/zqnt-utils/src/main/proto` in the `zqnt-platform` monorepo — the
same canonical `.proto` source every other language SDK/adapter in the platform uses (see
`buf.gen.yaml`). Run `make proto` to regenerate.

What changed as a result, if you're upgrading from before this migration:
- **`connector.ConnectorService`**: Mission/Task/Scheduler CRUD methods are gone (the backend
  doesn't have them anymore — replaced by `missionautonomy`'s Application/SkillExecution model).
  Asset/Organization operations remain, reshaped to match the current `AssetProtoDTO` (mostly-optional
  pointer fields, `SubAssets`/`Payloads` replacing the old singular `SubAsset`, no more `Online`
  field on the DTO itself).
- **`missionautonomy.MissionAutonomyService`**: reduced to just `GetScheduler` — Application/
  SkillExecution administration is a console/platform-side concern, not something an edge adapter
  itself calls (mirrors edge-python-sdk's own already-migrated `MissionAutonomyClient` exactly).
- **`adapter.EdgeAdapter`**: unchanged — still the same interface your hardware integration
  implements. The wire mapping underneath it (`adapter/grpc/`) was fully rewritten against the
  current `CommandResponse`/`CapabilityState` schema, but nothing about the Go-level interface
  contract you implement against changed.
- A handful of RPCs new on the current schema (`StartRecording`, `StopRecording`,
  `LiveStreamSplitScreen`, `SendCustomCommand`, `PauseTask`, `ResumeTask`, inbound `RegisterAsset`/
  `DeregisterAsset`) have no `EdgeAdapter` interface method yet and are left unimplemented
  (return a clean `codes.Unimplemented`, same as any command your own adapter doesn't override) —
  a deliberate, separately-scoped follow-up, not a regression.

**This repo had zero test files before the `skillregistry` addition.** `skillregistry` has
coverage; the rest of the SDK (everything under `adapter/`, `connector/`, `livedata/`,
`missionautonomy/`) still does not — the migration above was verified by `go build`/`go vet`
across the whole repo plus careful field-by-field review against the real generated schema, not by
a test suite, since none exists.

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
