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

## Requirements

Go 1.24+. `go build ./...` / `go vet ./...` / `go test ./...` all pass as of this commit.

## Regenerating `gen/`

```
./scripts/gen_protos.sh
```

Checks out zqnt-protos' `1.3.0` tag (verifying it still resolves to the expected commit — fails
loudly rather than silently regenerating from a moved tag), regenerates `gen/`, and restores the
proto submodule to whatever it was checked out at before.
