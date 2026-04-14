GEN_DIR    := gen/proto
PROTO_LABEL := 1.0.0-PR3-6bf7cc5-SNAPSHOT  # update when protos change

.PHONY: help setup proto build vet tidy check clean update-proto

# ── default ──────────────────────────────────────────────────────────────────
help:
	@echo "Available targets:"
	@echo "  setup        Install buf and protoc plugins (run once)"
	@echo "  proto        Regenerate code from buf.build/zqnt/protos"
	@echo "  build        Compile the SDK and example"
	@echo "  vet          Run go vet"
	@echo "  tidy         go mod tidy"
	@echo "  check        proto + tidy + build + vet (full CI check)"
	@echo "  clean        Remove generated .go files"
	@echo "  update-proto Bump PROTO_LABEL and regenerate"

# ── setup (run once per machine) ─────────────────────────────────────────────
setup:
	@echo "→ Installing buf..."
	go install github.com/bufbuild/buf/cmd/buf@latest
	@echo "→ Done. Run 'make proto' next."

# ── proto generation ─────────────────────────────────────────────────────────
proto:
	@echo "→ Generating from buf.build/zqnt/protos:$(PROTO_LABEL)"
	buf generate
	@echo "→ Running go mod tidy after generation..."
	go mod tidy
	@echo "✓ Proto generation complete."

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
check: proto build vet
	@echo "✓ All checks passed."

# ── clean generated code ──────────────────────────────────────────────────────
clean:
	@echo "→ Removing generated files in $(GEN_DIR)..."
	find $(GEN_DIR) -name '*.go' -delete
	@echo "✓ Clean."

# ── update proto label ────────────────────────────────────────────────────────
# Usage: make update-proto LABEL=1.0.0-new-label
update-proto:
	@if [ -z "$(LABEL)" ]; then echo "Usage: make update-proto LABEL=<new-label>"; exit 1; fi
	sed -i 's|$(PROTO_LABEL)|$(LABEL)|g' buf.gen.yaml Makefile
	$(MAKE) proto
