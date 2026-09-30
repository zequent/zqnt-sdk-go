# zqnt-client-sdk-go

Go SDK for customer applications consuming the Zequent platform — the Go counterpart to
`client-java-sdk` / `client-python-sdk`.

## This branch: pinned to the 1.3.0 wire contract

`feature/v1.3.0-proto` tracks zqnt-protos' `1.3.0` tag specifically, not the current (`main`,
2.0.0) line — see zqnt-protos' README "Versioning" section for the platform-wide convention. A
bugfix here ships as a point release off this branch (e.g. `v1.3.1`) without touching `main`.

The two branches cover genuinely different RPC surfaces, not just a version-numbered copy of the
same code — `main`'s MissionAutonomyService surface is Application/SkillExecution
(`capability-execution-*.proto`, which doesn't exist at 1.3.0 at all); this branch's is real
Mission/Task CRUD + lifecycle instead, which `main` explicitly does not cover (its own README
used to note there was "nothing working to mirror" there — true for `main`, not for 1.3.0, where
`GetMission`/`CreateMission`/.../`StartTask`/`StopTask`/`PauseTask`/`ResumeTask` are all real
RPCs).

**Included on this branch:**
- `gen/` — generated protobuf/gRPC Go code for the canonical protocol at zqnt-protos' `1.3.0`
  tag, regenerated via `scripts/gen_protos.sh` (verifies the pinned tag hasn't moved before
  generating). No `capability-execution-*.proto`/`gen/execution/...` — those don't exist at 1.3.0.
- `connector/` — asset lookup; scheduler CRUD (`GetScheduler`/`CreateScheduler(s)`/
  `UpdateScheduler`/`DeleteScheduler(s)`/`DeleteSchedulersByTask` — the last one is 1.3.0-only,
  retired from ConnectorService on `main`; `ListSchedulers` is NOT here — ConnectorService has no
  such RPC at 1.3.0, see `missionautonomy.Client.ListSchedulers` instead); operational policies
  (`GetActivePoliciesByType`, `GetAllActivePolicies`); technical config (`GetTechnicalConfigs`).
  No Skill Registry (`ObserveSkillContract` etc. — `main`/2.0.0-only, doesn't exist in
  connector.proto at 1.3.0).
- `missionautonomy/` — Mission CRUD (`CreateMission`/`UpdateMission`/`GetMission`/
  `DeleteMission`), Task CRUD + lifecycle (`CreateTask`/`UpdateTask`/`GetTask`/
  `GetTaskByFlightID`/`DeleteTask`/`StartTask`/`StopTask`/`PauseTask`/`ResumeTask`), and
  `ListSchedulers` (optionally filtered by task ID — the 1.3.0-only filter main reserves). No
  Application/SkillExecution surface at all on this branch.
- `remotecontrol/` — unchanged from `main`; `RemoteControlService`'s command-gateway surface is
  identical at both contract versions.
- `livedata/` — unchanged from `main`; returns the raw gRPC server-streaming client rather than a
  typed dataclass, so it never needed touching for the `TaskEvent`/`CommandExecutionEvent`
  drift that affected the Python SDKs' equivalent layer.

**Deliberately not included (same as `main`):**
- **Auto-reconnecting streams.** `StreamTelemetry`/`StreamDetections`/`StreamNotifications` return
  the raw `grpc.ServerStreamingClient` — redialing on a broken stream is the caller's job.
- A shared proto module: `gen/` is vendored directly into this repo rather than pulled from one
  common `zqnt-utils-go`.

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
assets := connector.New(conn)
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

Go 1.24+. `go build ./...` / `go vet ./...` / `go test ./...` all pass as of this commit.

## Regenerating `gen/`

```
./scripts/gen_protos.sh
```

Checks out zqnt-protos' `1.3.0` tag (verifying it still resolves to the expected commit — fails
loudly rather than silently regenerating from a moved tag), regenerates `gen/`, and restores the
proto submodule to whatever it was checked out at before.
