GEN_DIR := gen/proto

.PHONY: help setup proto build vet tidy check clean

# ── default ──────────────────────────────────────────────────────────────────
help:
	@echo "Available targets:"
	@echo "  setup   Install buf and protoc plugins (run once)"
	@echo "  proto   Regenerate code from proto/ submodule"
	@echo "  build   Compile the SDK and example"
	@echo "  vet     Run go vet"
	@echo "  tidy    go mod tidy"
	@echo "  check   proto + build + vet (full CI check)"
	@echo "  clean   Remove generated .go files"

# ── setup (run once per machine) ─────────────────────────────────────────────
setup:
	@echo "→ Installing buf..."
	go install github.com/bufbuild/buf/cmd/buf@latest
	@echo "→ Installing protoc-gen-go..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@echo "→ Installing protoc-gen-go-grpc..."
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "✓ Done. Run 'make proto' next."

# ── proto generation ─────────────────────────────────────────────────────────
proto:
	@echo "→ Generating from proto/ submodule..."
	@mkdir -p $(GEN_DIR)
	buf generate
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
