package session

import (
	"crypto/ecdh"
	"errors"
	"fmt"
	"time"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// VerifyWelcome checks a Welcome as the answer to the Hello with transcript helloTranscript: the
// platform key certificate against the trust store, the platform's signature, and the key
// agreement with the Hello's ephemeral key. It returns the session keys.
func VerifyWelcome(trust Trust, now time.Time, trusted bool, helloTranscript []byte, ephemeral *ecdh.PrivateKey,
	suite uint32, w *edgelinkv3.Welcome,
) (*handshake.SessionKeys, error) {
	pk := w.GetPlatformKey()
	if err := trust.VerifyPlatformKey(pk, now, trusted); err != nil {
		return nil, err
	}
	transcript := handshake.WelcomeTranscript(handshake.Hash(helloTranscript), w)
	if err := handshake.Verify(uint8(pk.GetKeyAlg()), pk.GetPublicKey(), transcript, w.GetSignature()); err != nil {
		return nil, fmt.Errorf("session: Welcome: %w", err)
	}
	shared, err := handshake.SharedSecret(ephemeral, w.GetEphemeralPub())
	if err != nil {
		return nil, fmt.Errorf("session: Welcome key agreement: %w", err)
	}
	defer clear(shared)
	return handshake.DeriveSessionKeys(suite, shared, helloTranscript, transcript)
}

// VerifyHelloRetry checks a HelloRetry as the answer to the Hello whose transcript hashes to
// helloHash.
func VerifyHelloRetry(trust Trust, now time.Time, trusted bool, helloHash []byte, r *edgelinkv3.HelloRetry) error {
	pk := r.GetPlatformKey()
	if err := trust.VerifyPlatformKey(pk, now, trusted); err != nil {
		return err
	}
	transcript, err := handshake.HelloRetryTranscript(helloHash, r)
	if err != nil {
		return err
	}
	if err := handshake.Verify(uint8(pk.GetKeyAlg()), pk.GetPublicKey(), transcript, r.GetSignature()); err != nil {
		return fmt.Errorf("session: HelloRetry: %w", err)
	}
	if len(r.GetServerNonce()) == 0 || r.GetPlatformTime() == nil {
		return errors.New("session: HelloRetry without nonce or platform time")
	}
	return nil
}
