# zqnt-client-sdk-go

Go SDK for customer applications consuming the Zequent platform — the Go counterpart to
`client-java-sdk` / `client-python-sdk`.

```
go get github.com/zequent/zqnt-client-sdk-go/v3
```

## Commanding assets (3.x): `control`

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
`zqnt-api-go` module (`github.com/zequent/zqnt-api-go/v3/gen/zqnt/...`), the same Go types the edge
SDK, the simulator and the Integration Hub use.

`examples/command` lists an asset's capabilities and runs one:
`go run ./examples/command DRONE-1 flight.takeoff '{"altitude": 40}'`.

### Manual control

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
`edgev3` is `github.com/zequent/zqnt-api-go/v3/gen/zqnt/edge/v3`.

## Telemetry, detections and alerts (3.x): `telemetry`

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

## Packages

- `control/` — the 3.x command API and manual control above.
- `telemetry/` — 3.x telemetry samples and source status, detections and alerts.
- `connector/` — asset lookup, the Skill Registry, scheduler CRUD, operational policies and
  technical config (2.x contract, still served).
- `missionautonomy/` — Applications and the SkillExecution lifecycle (2.x contract, still served).
- `auth/`, `config/` — the client credential and where the platform is, shared by every package.

Generated types come from
[zqnt-api-go](https://github.com/zequent/zqnt-api-go): `gen/<service>/proto` for the
2.x contracts, `gen/zqnt/<domain>/v3` for 3.x. This repository has no generated code of its own.

## Migrating from 2.x

3.0 offers only the command API: the 2.x packages `remotecontrol` (typed commands) and `livedata`
are gone. [MIGRATION.md](MIGRATION.md) maps every 2.x call to its command id or `telemetry` watch.

## Authentication

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

### Local development and deployment: `config`

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

### A credential that is not one fixed token

A service that forwards its own caller's token sets it per call — `auth.WithToken(ctx, token)` — or
registers its own interceptors ahead of the SDK's (`cfg.Dial(endpoint, grpc.WithChainUnaryInterceptor(...))`,
or before `auth.DialOptions` when dialing yourself; grpc-go runs chained interceptors in the order
given). An `authorization` header already on the call wins and the connection's token is then not sent.

## Requirements

Go 1.26+. `go build ./...`, `go vet ./...` and `go test ./...` pass (with a parent `go.work`, run
them with `GOWORK=off`).

## License

Licensed under the [Apache License, Version 2.0](LICENSE); see [NOTICE](NOTICE).
