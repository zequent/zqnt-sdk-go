.PHONY: help build vet test lint tidy check licences

help:
	@echo "Targets: build vet test lint tidy check (build + vet + test, what CI runs besides lint) licences"

build:
	go build ./...

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run --timeout=5m ./...

tidy:
	go mod tidy

check: build vet test

licences:
	scripts/licence-gate.sh
