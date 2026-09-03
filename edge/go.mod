module github.com/Zequent/zqnt-edge-sdk-go

go 1.26

require (
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/zequent/zqnt-utils-golang v1.3.0
	google.golang.org/grpc v1.78.0
	google.golang.org/protobuf v1.36.11
)

// zqnt-utils-golang v1.3.0's tag exists locally but hasn't been pushed to origin yet -- this
// points go.mod at the local sibling checkout in the meantime. Remove once the tag is pushed and
// `go mod tidy` can resolve it from GitHub directly.
replace github.com/zequent/zqnt-utils-golang => ../../../utils/zqnt-utils-golang

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251029180050-ab9386a59fda // indirect
)
