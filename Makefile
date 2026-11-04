.PHONY: help build vet test test-tpm lint tidy check licences

help:
	@echo "Targets: build vet test test-tpm lint tidy check (build + vet + test, what CI runs besides lint) licences"

build:
	go build ./...

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

# The TPM key store against the TPM 2.0 reference simulator (cgo, libcrypto); a module of its own.
test-tpm:
	cd edge/runtime/identity/tpmsim && go test -count=1 ./...

lint:
	golangci-lint run --timeout=5m ./...

tidy:
	go mod tidy

check: build vet test

licences:
	scripts/licence-gate.sh
