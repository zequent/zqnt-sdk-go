// Package tlsbinding computes the TLS channel binding every node-signed transcript carries: the
// "tls-exporter" binding of RFC 9266 for the TLS 1.3 connection a gRPC call travels on.
package tlsbinding

import (
	"crypto/tls"
	"errors"
	"fmt"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// Label is the exporter label of RFC 9266.
const Label = "EXPORTER-Channel-Binding"

// Size is the length of the binding.
const Size = 32

// ErrNoTLS is returned for a connection without TLS; its binding is empty.
var ErrNoTLS = errors.New("tlsbinding: connection has no TLS")

// FromState exports the binding of a TLS 1.3 connection.
func FromState(state tls.ConnectionState) ([]byte, error) {
	if state.Version != tls.VersionTLS13 {
		return nil, fmt.Errorf("tlsbinding: TLS version %#x, want TLS 1.3", state.Version)
	}
	return state.ExportKeyingMaterial(Label, nil, Size)
}

// FromPeer exports the binding of the connection p was reached over; ErrNoTLS when it has no TLS.
func FromPeer(p *peer.Peer) ([]byte, error) {
	if p == nil {
		return nil, errors.New("tlsbinding: no peer")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, ErrNoTLS
	}
	return FromState(info.State)
}
