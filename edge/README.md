# Edge Go SDK — Developer Setup

## Module

```
github.com/Zequent/zqnt-edge-sdk-go
```

## Prerequisites

- **Go 1.24+** — `go version` to verify
- **buf CLI** — installed via `make setup` (see below)

---

## 1. buf.build Authentication

The SDK proto definitions are hosted on the private Buf Schema Registry (BSR) at `buf.build/zqnt/protos`. You need a personal token to access it.

**Create a token:**

1. Log in to [buf.build](https://buf.build)
2. Go to **Settings → Tokens → Create Token**
3. Give it a name (e.g. `local-dev`) and copy the token

**Authenticate the CLI:**

```bash
buf registry login buf.build
# Paste your token when prompted
```

This writes credentials to `~/.netrc`, which buf and Go both use automatically.

---

## 2. Configure Go for Private BSR Modules

The BSR modules (`buf.build/gen/go/...`) are private, so Go's public checksum database and proxy cannot resolve them. Add these to your Go environment — run once per machine:

```bash
go env -w GOPRIVATE="buf.build/*"
go env -w GONOSUMDB="buf.build/*"
go env -w GONOPROXY="buf.build/*"
```

This tells Go to fetch `buf.build/*` modules directly (bypassing `proxy.golang.org` and `sum.golang.org`).

> **Persistent alternative:** Add to your shell profile (`~/.zshrc` / `~/.bashrc`):
> ```bash
> export GOPRIVATE="buf.build/*"
> export GONOSUMDB="buf.build/*"
> export GONOPROXY="buf.build/*"
> ```

---

## 3. Install buf CLI

```bash
make setup
```

This runs `go install github.com/bufbuild/buf/cmd/buf@latest`.

---

## 4. Download Go Dependencies

The `vendor/` directory is committed and includes all dependencies (including BSR-generated modules), so no `go get` is needed for a normal build.

If you need to update or re-download the BSR modules manually:

```bash
go get buf.build/gen/go/zqnt/protos/protocolbuffers/go@latest
go get buf.build/gen/go/zqnt/protos/grpc/go@latest
go mod vendor
```

---

## 5. Regenerate Proto Code

If the proto definitions at `buf.build/zqnt/protos` have changed (new label), regenerate:

```bash
make proto
```

This runs `buf generate` using `buf.gen.yaml` and outputs generated files to `gen/proto/`. To bump to a new proto label:

```bash
make update-proto LABEL=<new-label>
```

---

## 6. Build

```bash
make build
# or
go build ./...
```

To run all checks (proto + build + vet) as CI does:

```bash
make check
```

---

## Quick Reference

| Command | What it does |
|---------|-------------|
| `make setup` | Install buf CLI (once per machine) |
| `make proto` | Regenerate code from BSR protos |
| `make build` | Compile the SDK and example |
| `make vet` | Run `go vet` |
| `make tidy` | Run `go mod tidy` |
| `make check` | proto + build + vet (full CI check) |
| `make clean` | Remove generated `.go` files in `gen/proto/` |
| `make update-proto LABEL=<label>` | Bump proto label and regenerate |

---

## Troubleshooting

**`410 Gone` or `404` when downloading BSR modules**
- Your Go environment is routing through the public proxy. Verify `GOPRIVATE` is set: `go env GOPRIVATE`

**`buf generate` fails with auth error**
- Re-run `buf registry login buf.build` with a valid token

**`verifying module: ... not found` from sum.golang.org**
- `GONOSUMDB` is not set for `buf.build/*`. See step 2.

**Module not found in vendor**
- Run `go mod vendor` to re-sync the vendor directory after any `go.mod` changes

---

## Customer / Integrator Setup

This SDK is consumed as a standard Go module from `github.com/Zequent/zqnt-edge-sdk-go`.

**1. Configure Go for private modules** (both GitHub and BSR — run once per machine):

```bash
go env -w GOPRIVATE="github.com/Zequent/*,buf.build/*"
go env -w GONOSUMDB="github.com/Zequent/*,buf.build/*"
go env -w GONOPROXY="github.com/Zequent/*,buf.build/*"
```

**2. Authenticate with buf.build** (needed to resolve transitive BSR dependencies):

```bash
buf registry login buf.build
# Enter your buf.build token when prompted
```

**3. Add the SDK to your project:**

```bash
go get github.com/Zequent/zqnt-edge-sdk-go@v<version>
```

**4. Use it:**

```go
import (
    edgesdk "github.com/Zequent/zqnt-edge-sdk-go"
    "github.com/Zequent/zqnt-edge-sdk-go/adapter"
    "github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

type MyAdapter struct {
    adapter.UnimplementedEdgeAdapter
}

client, err := edgesdk.NewEdgeClient(backendAddr, deviceSN, &MyAdapter{})
```

See `example/main.go` for a full working example.
