.PHONY: help build vet tidy check

# ── default ──────────────────────────────────────────────────────────────────
help:
	@echo "Available targets:"
	@echo "  build   Compile the SDK and example"
	@echo "  vet     Run go vet"
	@echo "  tidy    go mod tidy"
	@echo "  check   build + vet (what CI runs)"
	@echo ""
	@echo "The v1.3.0-compat simulator built on this SDK lives in its own repo:"
	@echo "  github.com/Zequent/zqnt-simulator"

# No local proto generation: the stubs come from github.com/zequent/zqnt-api-go/v3.

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
