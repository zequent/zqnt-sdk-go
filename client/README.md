# zqnt-client-sdk-go

Go SDK for customer applications consuming the Zequent platform — the Go counterpart to
`client-java-sdk` / `client-python-sdk`.

## This branch: the 2.0 (v2) wire contract

`refactoring/refactoring-client-go-sdk-v2` generates `gen/` from zqnt-protos' v2 line (branch
`refactoring/refactoring-ecosystem-v2`), carried as this repo's own `proto/` submodule and pinned by
commit — the same commit zqnt-utils-golang generates from. The 1.3.x line (Mission/Task) lives on
`main` / `feature/v1.3.0-proto`; see zqnt-protos' README "Versioning" section.

**Included:**
- `gen/` — generated protobuf/gRPC Go code for every `.proto` in `proto/`, regenerated via
  `scripts/gen_protos.sh` (refuses to run unless the submodule is at the pinned commit).
- `connector/` — asset lookup, the Skill Registry (`ObserveSkillContract`, `ListSkillContracts`,
  `SetSkillContractStatus`, `SetSkillContractPermissions`), scheduler CRUD (`GetScheduler`/
  `ListSchedulers`/`CreateScheduler(s)`/`UpdateScheduler`/`DeleteScheduler(s)`), operational policies
  (`GetActivePoliciesByType`, `GetAllActivePolicies`) and technical config (`GetTechnicalConfigs`).
- `missionautonomy/` — Application CRUD + `ListApplications`, the SkillExecution lifecycle
  (`CreateSimpleExecution`/`ExecuteSimple` for a single command, `CreateApplicationExecution`/
  `ExecuteApplication` for a named Skill out of a deployed Application, `GetSkillExecution`,
  `ListSkillExecutions`, `Start`/`Pause`/`Resume`/`CancelSkillExecution`, `SignalSkillExecution`) and
  `ResolveExecutionConfig`. There is no Mission/Task surface: the 2.0 contract has no such RPCs.
- `remotecontrol/` — `RemoteControlService`'s command gateway. `GoToWithOptions(ctx, sn, coordinate,
  GoToOptions{NoFlyZoneOverride: true})` flies through a no-fly zone that would refuse the fly-to
  (honoured for an organization admin or a system admin only); `GoTo` is that with no options.
- `livedata/` — telemetry/detection/notification streaming, returning the raw gRPC server-streaming
  client.

**Deliberately not included:**
- **Auto-reconnecting streams.** `StreamTelemetry`/`StreamDetections`/`StreamNotifications` return
  the raw `grpc.ServerStreamingClient` — redialing on a broken stream is the caller's job.
- A shared proto module: `gen/` is vendored directly into this repo rather than pulled from
  zqnt-utils-golang.

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

Generates from the `proto/` submodule (`git submodule update --init proto` in a fresh clone),
after verifying it is at the commit pinned in the script. To move to a newer contract, check the
submodule out at the new commit, update `EXPECTED_PROTO_COMMIT` in the script, regenerate, and commit
the submodule pointer together with `gen/`.
