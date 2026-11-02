// Package tpmsim tests the TPM 2.0 key store against the TPM 2.0 reference simulator
// (go-tpm-tools, cgo and libcrypto). It is a module of its own so the simulator never becomes a
// dependency of the SDK: cd edge/runtime/identity/tpmsim && go test ./...
package tpmsim
