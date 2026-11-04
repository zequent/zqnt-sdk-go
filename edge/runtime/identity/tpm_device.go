//go:build !windows

package identity

import (
	"github.com/google/go-tpm/tpm2/transport"
	"github.com/google/go-tpm/tpm2/transport/linuxtpm"
)

func openTPM(device string) (transport.TPMCloser, error) { return linuxtpm.Open(device) }
