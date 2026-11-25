module github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/tpmsim

go 1.26.2

require (
	github.com/google/go-tpm v0.9.8
	github.com/zequent/zqnt-api/go/v3 v3.0.1-0.20261102064010-50810d6799df
	github.com/zequent/zqnt-sdk-go/v3 v3.0.0
)

require (
	github.com/google/go-tpm-tools v0.4.11
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/zequent/zqnt-sdk-go/v3 => ../../../..
