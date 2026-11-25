#!/usr/bin/env bash
# Licence gate: lists every dependency (with tests, for each target OS) with go-licenses and
# checks it against licences/policy.yaml. Writes THIRD_PARTY.md; with --check (CI) it fails
# instead when THIRD_PARTY.md is out of date.
#
# Needs go-licenses (go install github.com/google/go-licenses/v2@v2.0.1) and uv.
set -euo pipefail

cd "$(dirname "$0")/.."
MODULE=$(go list -m)
GO_LICENSES=${GO_LICENSES:-go-licenses}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

for os in linux darwin windows; do
  if ! GOOS=$os "$GO_LICENSES" report ./... --include_tests --ignore "$MODULE" \
    >"$WORK/$os.csv" 2>"$WORK/$os.log"; then
    cat "$WORK/$os.log" >&2
    exit 1
  fi
done

uv run --quiet --no-project --with pyyaml==6.0.3 python scripts/licence_gate.py \
  --ecosystem go "$@" "$WORK"/*.csv
