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

It's generated from the **current** proto schema (`gen/connector/proto`), not the `proto/` submodule
pinned by the rest of this SDK — see "Two proto generations" below for why.

### Two proto generations, on purpose

This SDK's `proto/` submodule (`zqnt-protos`) is pinned to a schema that predates the platform's
Skill/Capability/Application model — its `connector.proto`/`edge.proto` don't have
`ObserveSkillContract`, `ListSkillContracts`, or the 5-value `CapabilityState` enum at all; they're
still on a plain `bool available` for the old `Capability`/`GetCapabilities` message pair used by
`adapter/grpc/server.go`. Rather than bump the submodule (a real, coordinated breaking change to a
dependency this repo doesn't own) just to add one new package, `skillregistry` vendors its own
up-to-date generated code under `gen/connector/...`, `gen/common/...`, etc. — alongside, not
replacing, the submodule-generated `gen/proto`. The two coexist without conflict (different Go
package paths) and `make proto` / the `proto/` submodule are untouched.

Two consequences worth knowing:
- **`GetCapabilities`'s wire format is the older, 2-value schema** (`available bool`, not
  `CapabilityState`) until the submodule itself is bumped — that's a separate, bigger change
  (updating `zqnt-protos`, then regenerating and testing this SDK's existing surface against it)
  that wasn't done here.
- **This repo had zero test files before this change.** `skillregistry` has coverage; the rest of
  the SDK (everything under `adapter/`, `connector/`, `livedata/`, `missionautonomy/`) does not.

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
