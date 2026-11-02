package identity

import (
	"errors"

	"github.com/google/go-tpm/tpm2/transport"
)

func openTPM(string) (transport.TPMCloser, error) {
	return nil, errors.New("TPM key store is not supported on Windows")
}
