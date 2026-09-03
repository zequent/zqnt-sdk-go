// This is its own Go module, deliberately -- it depends on github.com/Zequent/zqnt-edge-sdk-go
// as a real, tagged, external dependency (the v1.3.0-compat release) rather than sharing the SDK
// repo's own module via same-module relative imports. That makes this the first real consumer of
// the v1.3.0-compat tag exactly the way a customer would use it: `go get`, not a local path.
module github.com/Zequent/zqnt-edge-sdk-go-simulator

go 1.26

require (
	github.com/Zequent/zqnt-edge-sdk-go v1.3.0-compat
	github.com/redis/go-redis/v9 v9.22.0
	github.com/zequent/zqnt-utils-golang v1.3.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251029180050-ab9386a59fda // indirect
	google.golang.org/grpc v1.78.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
