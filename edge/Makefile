.PHONY: help build vet tidy check

# ── default ──────────────────────────────────────────────────────────────────
help:
	@echo "Available targets:"
	@echo "  build   Compile the SDK, example and simulator"
	@echo "  vet     Run go vet"
	@echo "  tidy    go mod tidy"
	@echo "  check   build + vet (what CI runs)"

# v1.3.0-compat branch: no local proto generation here anymore -- proto stubs come from
# github.com/zequent/zqnt-utils-golang v1.3.0 (a dependency, not generated in this repo), pinned
# to the exact zqnt-protos commit zqnt-utils-java:1.3.0 depends on. Regenerating that pin is
# zqnt-utils-golang's own scripts/gen_protos.sh, not something this Makefile does.

# ── build ─────────────────────────────────────────────────────────────────────
build:
	go build ./...

# ── vet ───────────────────────────────────────────────────────────────────────
vet:
	go vet ./...

# ── tidy ──────────────────────────────────────────────────────────────────────
tidy:
	go mod tidy

# ── full check (what CI runs) ─────────────────────────────────────────────────
check: build vet
	@echo "✓ All checks passed."
