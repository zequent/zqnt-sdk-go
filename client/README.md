# zqnt-client-sdk-go

Go SDK for customer applications consuming the Zequent platform — the Go counterpart to
`client-java-sdk` / `client-python-sdk`. Built locally in this session; **not yet pushed
anywhere** — create the `Zequent/zqnt-client-sdk-go` GitHub repo, then
`git remote add origin <url> && git push -u origin main`.

## Status: first slice, not yet at parity with the Java/Python Client SDKs

This is a real, tested foundation — not a stub — but it covers a deliberately narrow slice:

**Included:**
- `gen/` — generated protobuf/gRPC Go code for the full canonical protocol (`utils/zqnt-utils/src/main/proto/*.proto`).
- `connector/` — `GetAssetBySn` plus the full Skill Registry surface (`ObserveSkillContract`, `ListSkillContracts`, `SetSkillContractStatus`, `SetSkillContractPermissions`) — the persisted, de-duplicated capability catalog, independent of which devices are currently connected.
- `missionautonomy/` — Application CRUD (`UpsertApplication`/`GetApplication`/`DeleteApplication`) and the full SkillExecution lifecycle (`CreateSimpleExecution`, `ExecuteSimple`, `GetSkillExecution`, `Start`/`Pause`/`Resume`/`CancelSkillExecution`, `SignalSkillExecution`).

**Not included yet (real gaps, not just "someday"):**
- No `RemoteControl` client (manual control / live monitoring — WebSocket-based in the Java SDK, would need its own transport here, not just gRPC).
- No `LiveData` client (telemetry/detection streaming).
- No `ListApplications`, scheduler, technical-config, or policy RPCs on the `connector`/`missionautonomy` clients — only the subset above.
- `ApplicationExecutionSpecProto` (running a named Application/Skill rather than a single ad-hoc command) isn't wired into `missionautonomy` yet — only `SimpleExecutionSpecProto` is.
- The generated `gen/` tree is vendored directly into this repo rather than pulled from a shared proto module (no `zqnt-utils-go` exists yet) — future proto changes must be regenerated and copied in by hand until that's addressed. See `edge-go-sdk`'s README for the exact `protoc` invocation used to generate this.

## Requirements

Go 1.24+. `go build ./...` / `go test ./...` both pass as of this commit.
