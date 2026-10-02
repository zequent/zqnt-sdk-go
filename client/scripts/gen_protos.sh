#!/usr/bin/env bash
# Generate Go protobuf / gRPC stubs from the platform's canonical proto source: zqnt-protos, which
# this repo carries as its own `proto/` submodule -- the same layout zqnt-utils-golang uses. A plain
# protoc invocation, no buf. This repo has no shared proto module dependency: gen/ is vendored
# directly, per its own README.
#
# Owning the submodule, rather than reaching sideways into a sibling checkout, means the generated
# output depends on one commit this repo records, generation works in any clone
# (`git submodule update --init`), and it never checks out or restores a shared checkout other
# services are using at the same time.
#
# To move to a new contract version: update the submodule (`git -C proto fetch && git -C proto
# checkout <commit>`), update EXPECTED_PROTO_COMMIT below to match, re-run this script, and commit
# the submodule pointer together with the regenerated gen/.
#
# Output: gen/ (module-relative -- each proto file's own `go_package` option, e.g.
# "gen/edge/sdk/proto", is what actually places its output; the -M mappings below just supply the
# full module prefix protoc-gen-go needs to resolve cross-file imports).
#
# Usage: ./scripts/gen_protos.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="$ROOT/proto"
OUT_DIR="$ROOT"
MODULE="github.com/Zequent/zqnt-client-sdk-go"

# The zqnt-protos commit gen/ is built from: the v2 line (branch refactoring/refactoring-ecosystem-v2),
# the same commit zqnt-utils-golang generates from. The submodule pointer is the real pin; this
# constant only asserts that what's checked out right now is still it, so a half-finished submodule
# bump can't silently regenerate everything against an unintended contract.
EXPECTED_PROTO_COMMIT="24ea6cb1703ef143f473b13c29249a807ae2b338"

if [ ! -f "$PROTO_DIR/common.proto" ]; then
  echo "Proto submodule is not checked out at $PROTO_DIR -- run:" >&2
  echo "  git submodule update --init proto" >&2
  exit 1
fi

CURRENT_COMMIT="$(git -C "$PROTO_DIR" rev-parse HEAD)"
if [ "$CURRENT_COMMIT" != "$EXPECTED_PROTO_COMMIT" ]; then
  echo "The proto submodule is at $CURRENT_COMMIT, not the expected" >&2
  echo "$EXPECTED_PROTO_COMMIT. Refusing to generate from an unverified contract: either run" >&2
  echo "'git submodule update proto' to restore the recorded commit, or -- if you are" >&2
  echo "deliberately moving to a new one -- update EXPECTED_PROTO_COMMIT in this script to match." >&2
  exit 1
fi
echo "Generating from zqnt-protos $CURRENT_COMMIT (contract $(cat "$PROTO_DIR/PROTOCOL_VERSION" 2>/dev/null || echo "unknown"))..."

# Identical file list to zqnt-utils-golang's gen_protos.sh, with this module's prefix.
M_MAPPINGS=(
  "asset.proto=$MODULE/gen/common/asset/proto"
  "base.proto=$MODULE/gen/common/base/proto"
  "common.proto=$MODULE/gen/common/proto"
  "connector.proto=$MODULE/gen/connector/proto"
  "detection.proto=$MODULE/gen/common/detection/proto"
  "device-control-contracts.proto=$MODULE/gen/devicecontrol/contracts/proto"
  "edge.proto=$MODULE/gen/edge/sdk/proto"
  "events.proto=$MODULE/gen/events/proto"
  "live-data-types.proto=$MODULE/gen/livedata/proto"
  "live-data.proto=$MODULE/gen/livedata/proto"
  "media.proto=$MODULE/gen/media/proto"
  "mission-autonomy-contracts.proto=$MODULE/gen/missionautonomy/contracts/proto"
  "mission-autonomy-dto.proto=$MODULE/gen/missionautonomy/dto/proto"
  "mission-autonomy-types.proto=$MODULE/gen/missionautonomy/domain/types/proto"
  "mission-autonomy.proto=$MODULE/gen/missionautonomy/proto"
  "remote-control.proto=$MODULE/gen/remotecontrol/proto"
  "simulator-control.proto=$MODULE/gen/simulatorcontrol/proto"
  "capability-execution-contracts.proto=$MODULE/gen/execution/contracts/proto"
  "capability-execution-types.proto=$MODULE/gen/execution/domain/types/proto"
  "capability-execution-dto.proto=$MODULE/gen/execution/dto/proto"
)

GO_OPTS=("paths=import" "module=$MODULE")
for m in "${M_MAPPINGS[@]}"; do
  GO_OPTS+=("M$m")
done

PROTO_FILES=("$PROTO_DIR"/*.proto)
echo "Generating ${#PROTO_FILES[@]} proto file(s) -> $OUT_DIR/gen/"

# Start from an empty gen/ so a proto file or message removed upstream leaves no stale stubs behind.
rm -rf "$OUT_DIR/gen"

go_opt_args=()
for o in "${GO_OPTS[@]}"; do go_opt_args+=("--go_opt=$o"); done
go_grpc_opt_args=()
for o in "${GO_OPTS[@]}"; do go_grpc_opt_args+=("--go-grpc_opt=$o"); done
go_grpc_opt_args+=("--go-grpc_opt=require_unimplemented_servers=true")

protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$OUT_DIR" "${go_opt_args[@]}" \
  --go-grpc_out="$OUT_DIR" "${go_grpc_opt_args[@]}" \
  "${PROTO_FILES[@]}"

echo "Done."
