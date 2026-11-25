package session_test

import (
	"bytes"
	"crypto/ecdh"
	"errors"
	"testing"
	"time"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/identitytest"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/session"
)

func vectorTrust(t *testing.T, v *identitytest.Vectors) *identity.Trust {
	t.Helper()
	dir := identity.Dir(t.TempDir())
	state := &identity.State{
		Version:        1,
		NodeID:         v.Inputs.NodeID,
		OrganizationID: v.Inputs.OrganizationID,
		InstallationID: v.Inputs.InstallationID,
		NodeClass:      identity.ClassInfrastructure,
		Anchors:        []identity.Anchor{{KeyID: identity.HexBytes(v.Keys.AnchorKeyID), Alg: 2, PublicKey: v.Keys.AnchorPublic}},
		Suite:          0x0101,
		IdentityKeyID:  identity.HexBytes(v.Keys.NodeIdentityKeyID),
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	trust, err := dir.Trust(state)
	if err != nil {
		t.Fatal(err)
	}
	return trust
}

func TestVerifyWelcomeVector(t *testing.T) {
	v := identitytest.LoadVectors(t)
	trust := vectorTrust(t, v)
	eph, err := ecdh.X25519().NewPrivateKey(v.Inputs.NodeEphemeralPrivate)
	if err != nil {
		t.Fatal(err)
	}
	inWindow := time.Unix(v.PlatformKeyCertificate.NotBefore+3600, 0)

	keys, err := session.VerifyWelcome(trust, inWindow, true, v.Hello.Transcript, eph, 0x0101, v.WelcomeFrame())
	if err != nil {
		t.Fatal(err)
	}
	ks := v.KeySchedule
	for name, c := range map[string][2][]byte{
		"th": {keys.TranscriptHash, ks.TranscriptHash}, "key_n2p": {keys.KeyN2P, ks.KeyN2P}, "key_p2n": {keys.KeyP2N, ks.KeyP2N},
		"iv_n2p": {keys.IVN2P, ks.IVN2P}, "iv_p2n": {keys.IVP2N, ks.IVP2N}, "exporter": {keys.Exporter, ks.Exporter},
	} {
		if !bytes.Equal(c[0], c[1]) {
			t.Errorf("%s = %x, want %x", name, c[0], c[1])
		}
	}

	if _, err := session.VerifyWelcome(trust, inWindow, false, v.Hello.Transcript, eph, 0x0101, v.WelcomeFrame()); err != nil {
		t.Errorf("untrusted clock, no high-water mark: %v", err)
	}
	after := time.Unix(v.PlatformKeyCertificate.NotAfter+1, 0)
	if _, err := session.VerifyWelcome(trust, after, true, v.Hello.Transcript, eph, 0x0101, v.WelcomeFrame()); !errors.Is(err, identity.ErrPlatformKeyTime) {
		t.Errorf("expired platform key with trusted time: %v", err)
	}
	if err := trust.RaiseHighWater(after); err != nil {
		t.Fatal(err)
	}
	if _, err := session.VerifyWelcome(trust, time.Unix(0, 0), false, v.Hello.Transcript, eph, 0x0101, v.WelcomeFrame()); !errors.Is(err, identity.ErrPlatformKeyTime) {
		t.Errorf("platform key older than the high-water mark: %v", err)
	}
}

func TestVerifyWelcomeVectorRefusals(t *testing.T) {
	v := identitytest.LoadVectors(t)
	trust := vectorTrust(t, v)
	eph, err := ecdh.X25519().NewPrivateKey(v.Inputs.NodeEphemeralPrivate)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(v.PlatformKeyCertificate.NotBefore+3600, 0)
	otherHello := append([]byte(nil), v.Hello.Transcript...)
	otherHello[len(otherHello)-1] ^= 1
	if _, err := session.VerifyWelcome(trust, now, true, otherHello, eph, 0x0101, v.WelcomeFrame()); !errors.Is(err, handshake.ErrSignature) {
		t.Errorf("Welcome for another Hello: %v", err)
	}

	w := v.WelcomeFrame()
	w.ClockOffsetMs = 0
	if _, err := session.VerifyWelcome(trust, now, true, v.Hello.Transcript, eph, 0x0101, w); !errors.Is(err, handshake.ErrSignature) {
		t.Errorf("changed clock offset: %v", err)
	}
	w = v.WelcomeFrame()
	w.PlatformKey.NotAfter++
	if _, err := session.VerifyWelcome(trust, now, true, v.Hello.Transcript, eph, 0x0101, w); !errors.Is(err, identity.ErrPlatformKey) {
		t.Errorf("extended platform key: %v", err)
	}
	w = v.WelcomeFrame()
	w.PlatformKey.InstallationId = "other"
	if _, err := session.VerifyWelcome(trust, now, true, v.Hello.Transcript, eph, 0x0101, w); !errors.Is(err, identity.ErrPlatformKey) {
		t.Errorf("other installation: %v", err)
	}
	w = v.WelcomeFrame()
	w.PlatformKey.AnchorKeyId = v.Keys.PlatformHandshakeKeyID
	if _, err := session.VerifyWelcome(trust, now, true, v.Hello.Transcript, eph, 0x0101, w); !errors.Is(err, identity.ErrUnknownAnchor) {
		t.Errorf("unpinned anchor: %v", err)
	}
	w = v.WelcomeFrame()
	w.EphemeralPub = make([]byte, 32)
	if _, err := session.VerifyWelcome(trust, now, true, v.Hello.Transcript, eph, 0x0101, w); err == nil {
		t.Error("accepted a low-order platform ephemeral key")
	}
}

func TestVerifyHelloRetryVector(t *testing.T) {
	v := identitytest.LoadVectors(t)
	trust := vectorTrust(t, v)
	now := time.Unix(v.PlatformKeyCertificate.NotBefore+3600, 0)
	hash := handshake.Hash(v.HelloRetry.FirstHelloTranscript)
	if err := session.VerifyHelloRetry(trust, now, false, hash, v.HelloRetryFrame()); err != nil {
		t.Fatal(err)
	}
	r := v.HelloRetryFrame()
	r.ServerNonce[0] ^= 1
	if err := session.VerifyHelloRetry(trust, now, false, hash, r); !errors.Is(err, handshake.ErrSignature) {
		t.Errorf("changed nonce: %v", err)
	}
	if err := session.VerifyHelloRetry(trust, now, false, handshake.Hash(v.Hello.Transcript), v.HelloRetryFrame()); !errors.Is(err, handshake.ErrSignature) {
		t.Errorf("retry for another Hello: %v", err)
	}
}
