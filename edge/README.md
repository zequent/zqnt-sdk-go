# zqnt-edge-sdk-go

Go SDK for connecting edge devices (drones, robots) to the Zequent platform via gRPC.

## Quick Start

**1. Add the SDK to your project:**

```bash
go env -w GONOSUMDB="github.com/Zequent/*,github.com/zequent/*"
go env -w GONOPROXY="github.com/Zequent/*,github.com/zequent/*"
go get github.com/Zequent/zqnt-edge-sdk-go@v1.3.0-compat  # this branch's tag; proto stubs come from github.com/zequent/zqnt-utils-golang@v1.3.0
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

## This branch: pinned to the v1.3.0 wire contract

`feature/v1.3.0-compat-simulator` intentionally targets an older wire contract than this SDK's
main/`feature/skill-registry-v2` line (the 2.0.0 track). It exists so customers still integrated
against `edge-java-sdk`'s real, published `v1.3.0` tag have a Go-side simulator that speaks the
exact same contract — not the current one, verified-compatible-by-diff-and-hope.

**Proto source**: no local generation in this repo anymore (no `buf.gen.yaml`, no `gen/`). Stubs
come from [`github.com/zequent/zqnt-utils-golang`](../../../utils/zqnt-utils-golang) `v1.3.0` — a
published, versioned dependency, mirroring how `edge-java-sdk` depends on `zqnt-utils-java` and
`edge-python-sdk` depends on the `zqnt-utils` pip package, rather than generating its own proto
code inline. That module's `v1.3.0` tag is pinned to `0e072f869b650f3c3f769b89a677f23e8a1b0766`,
the exact `zqnt-protos` commit `zqnt-utils-java:1.3.0` (and so `edge-java-sdk` v1.3.0) itself
depends on — see `utils/zqnt-utils`'s own `v1.3.0` tag. (The SDK's main/2.0.0-track line still
generates its own `gen/` locally via `buf generate` against the live monorepo proto checkout, no
pin at all — that's the thing this branch deliberately doesn't do.)

**Verified wire-compatible with the current 2.0.0-track proto** (diffed directly, not assumed):
`edge.proto`, `common.proto`, `live-data.proto`, `live-data-types.proto` are byte-identical between
the v1.3.0 pin and current HEAD — the entire `EdgeAdapterService`/telemetry surface hasn't moved at
all. `asset.proto` and `device-control-contracts.proto` differ only additively (new `AssetVendor`
value, new optional `Capability` fields the Skill Registry uses) — nothing this SDK's hand-written
code depends on. `connector.proto`/`mission-autonomy*.proto` differ substantially (the whole
Mission/Task/Scheduler CRUD → capability-execution rewrite), which is why `missionautonomy`'s
`SchedulerDTO` here uses `MissionID`/`TaskID` (the v1.3.0 shape), not the reshaped
`AssetSN`/`CommandID`/`ApplicationID`/`SkillID` fields the 2.0.0-track branch has, and why
`skillregistry/` doesn't exist on this branch at all — it has no v1.3.0 analog (Skill Registry is
new-schema-only), so it was removed rather than left to not compile.

**`adapter.EdgeAdapter`**: closed the one real gap found diffing this interface against
`EdgeAdapterService.java` at the real `v1.3.0` tag — `PauseTask`/`ResumeTask`/
`LiveStreamSplitScreen`/`SendCustomCommand` now have real interface methods (previously
NOT_IMPLEMENTED-only, `edge.proto`'s wire RPCs for them already existed but nothing routed to
them). Everything else in the interface already matched v1.3.0 field-for-field.

**This repo had zero test files before the `skillregistry` addition** (now removed on this
branch); the rest of the SDK still doesn't — verified by `go build`/`go vet` plus live-verifying
the `simulator/` against a real running connector/live-data/remote-control stack (register →
TakeOff/GoTo/ReturnToHome move it → live telemetry visible via `StreamTelemetry`), not by a test
suite.

**`simulator/` is its own Go module** (`simulator/go.mod`, module path
`github.com/Zequent/zqnt-edge-sdk-go-simulator`), not part of this repo's own module — it depends
on `github.com/Zequent/zqnt-edge-sdk-go@v1.3.0-compat` as a real tagged dependency, the same way an
actual customer building against this release would, rather than sharing this module via
same-module relative imports. Build/vet/run it from inside `simulator/`, not from the repo root
(`go build ./...` at the root deliberately excludes it — that's what having its own `go.mod` does).
A distinct module path was required: this repo's own already-tagged `v1.3.0-compat` release still
contains the `simulator/` source tree from before it got its own `go.mod`, so reusing the SDK's own
module path for it would be ambiguous — `go mod tidy` refuses with "ambiguous import" if the paths
collide.

```bash
cd simulator
go build ./...
go run ./cmd/simulator
```

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
