#!/usr/bin/env bash
# Generate Go protobuf / gRPC stubs from the platform's canonical proto source, pinned to
# zqnt-protos' `1.3.0` tag -- this repo's own feature/v1.3.0-proto branch is the client-go-sdk
# counterpart of zqnt-utils-golang's feature/v1.3.0-proto, mirroring how it generates from a
# sibling zqnt-utils checkout with a plain protoc invocation (this repo has no shared proto
# module dependency -- gen/ is vendored directly, per its own README).
#
# Source: ../../../utils/zqnt-utils/src/main/proto (this repo lives at
# zqnt-platform/sdks/client/client-go-sdk). That directory is a git submodule pointing at
# zqnt-protos, which carries its own version tags (see its README's "Versioning" section) -- this
# script pins to zqnt-protos' own `1.3.0` tag by name, generates, and restores the submodule to
# whatever it was checked out at before; it does not permanently move the shared monorepo
# checkout other services rely on.
#
# Output: gen/ (module-relative -- each proto file's own `go_package` option, e.g.
# "gen/edge/sdk/proto", is what actually places its output; the -M mappings below just supply the
# full module prefix protoc-gen-go needs to resolve cross-file imports).
#
# Usage: ./scripts/gen_protos.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="$(cd "$ROOT/../../../utils/zqnt-utils/src/main/proto" && pwd)"
OUT_DIR="$ROOT"
MODULE="github.com/Zequent/zqnt-client-sdk-go"

# zqnt-protos' own `1.3.0` tag -- see its README's Versioning section. Verified below rather than
# trusted blindly: an annotated tag is supposed to be immutable once published, so the real risk
# here isn't "can't fetch the tag" -- it's "the tag got moved". Resolve it, then assert it's still
# the exact commit zqnt-utils-java:1.3.0 (and thus edge-java-sdk v1.3.0) pins, and fail loudly if
# not, rather than silently generating from whatever it now points to.
PROTO_TAG="1.3.0"
PROTO_TAG_COMMIT="0e072f869b650f3c3f769b89a677f23e8a1b0766"

if [ ! -d "$PROTO_DIR" ]; then
  echo "Canonical proto source not found at $PROTO_DIR -- this script must be run from a" >&2
  echo "checkout of zqnt-platform, with client-go-sdk at sdks/client/client-go-sdk (i.e. a" >&2
  echo "descendant of the same monorepo utils/zqnt-utils lives in)." >&2
  exit 1
fi

ORIGINAL_COMMIT="$(git -C "$PROTO_DIR" rev-parse HEAD)"
if ! git -C "$PROTO_DIR" rev-parse --verify --quiet "refs/tags/$PROTO_TAG" >/dev/null; then
  echo "Fetching zqnt-protos tag $PROTO_TAG..."
  git -C "$PROTO_DIR" fetch --quiet origin "refs/tags/$PROTO_TAG:refs/tags/$PROTO_TAG"
fi

RESOLVED_COMMIT="$(git -C "$PROTO_DIR" rev-parse "refs/tags/$PROTO_TAG^{commit}")"
if [ "$RESOLVED_COMMIT" != "$PROTO_TAG_COMMIT" ]; then
  echo "zqnt-protos tag $PROTO_TAG resolves to $RESOLVED_COMMIT, not the expected" >&2
  echo "$PROTO_TAG_COMMIT -- the tag has moved since this script was last updated. Refusing to" >&2
  echo "generate from an unverified commit; update PROTO_TAG_COMMIT above once you've confirmed" >&2
  echo "the new target is actually what you want." >&2
  exit 1
fi
echo "Pinning proto submodule to zqnt-protos $PROTO_TAG ($RESOLVED_COMMIT, currently $ORIGINAL_COMMIT)..."
git -C "$PROTO_DIR" checkout --quiet "$PROTO_TAG"

restore() {
  echo "Restoring proto submodule to $ORIGINAL_COMMIT..."
  git -C "$PROTO_DIR" checkout --quiet "$ORIGINAL_COMMIT"
}
trap restore EXIT

# capability-execution-*.proto don't exist at 1.3.0 -- no mapping for them here (that's the whole
# point of this branch). See zqnt-utils-golang's own gen_protos.sh for the identical file list.
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
  "mission-autonomy-contracts.proto=$MODULE/gen/missionautonomy/contracts/proto"
  "mission-autonomy-dto.proto=$MODULE/gen/missionautonomy/dto/proto"
  "mission-autonomy-types.proto=$MODULE/gen/missionautonomy/domain/types/proto"
  "mission-autonomy.proto=$MODULE/gen/missionautonomy/proto"
  "remote-control.proto=$MODULE/gen/remotecontrol/proto"
)

GO_OPTS=("paths=import" "module=$MODULE")
for m in "${M_MAPPINGS[@]}"; do
  GO_OPTS+=("M$m")
done

PROTO_FILES=("$PROTO_DIR"/*.proto)
echo "Generating ${#PROTO_FILES[@]} proto file(s) -> $OUT_DIR/gen/"

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
