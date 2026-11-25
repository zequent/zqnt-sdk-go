package identitytest

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Hex is a hex string in the vectors file.
type Hex []byte

func (h *Hex) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := hex.DecodeString(s)
	*h = v
	return err
}

// Signed is a transcript and its signature.
type Signed struct {
	Transcript Hex `json:"transcript"`
	Signature  Hex `json:"signature"`
}

// Vectors are the test vectors of spec edge-link-handshake-v1 (identity/testdata, a copy of the
// zqnt-api file, never regenerated).
type Vectors struct {
	Inputs struct {
		ClaimCode            string `json:"claim_code"`
		EnrollmentChallenge  Hex    `json:"enrollment_challenge"`
		InstallationID       string `json:"installation_id"`
		NodeEphemeralPrivate Hex    `json:"node_ephemeral_private"`
		NodeID               string `json:"node_id"`
		NodeIdentityPrivate  Hex    `json:"node_identity_private"`
		OrganizationID       string `json:"organization_id"`
		RotationNewPrivate   Hex    `json:"rotation_new_identity_private"`
		ServerNonce          Hex    `json:"server_nonce"`
		TLSChannelBinding    Hex    `json:"tls_channel_binding"`
	} `json:"inputs"`
	Keys struct {
		AnchorKeyID             Hex `json:"anchor_key_id"`
		AnchorPublic            Hex `json:"anchor_public"`
		NodeIdentityKeyID       Hex `json:"node_identity_key_id"`
		NodeIdentityPublic      Hex `json:"node_identity_public"`
		PlatformEphemeralPublic Hex `json:"platform_ephemeral_public"`
		PlatformHandshakeKeyID  Hex `json:"platform_handshake_key_id"`
		PlatformHandshakePublic Hex `json:"platform_handshake_public"`
		RotationNewKeyID        Hex `json:"rotation_new_identity_key_id"`
		RotationNewPublic       Hex `json:"rotation_new_identity_public"`
	} `json:"keys"`
	PlatformKeyCertificate struct {
		Signed
		NotBefore int64 `json:"not_before"`
		NotAfter  int64 `json:"not_after"`
	} `json:"platform_key_certificate"`
	Hello struct {
		Signed
		TranscriptSHA256 Hex `json:"transcript_sha256"`
	} `json:"hello"`
	Welcome     Signed `json:"welcome"`
	KeySchedule struct {
		Exporter       Hex `json:"exporter"`
		IVN2P          Hex `json:"iv_n2p"`
		IVP2N          Hex `json:"iv_p2n"`
		KeyN2P         Hex `json:"key_n2p"`
		KeyP2N         Hex `json:"key_p2n"`
		TranscriptHash Hex `json:"transcript_hash"`
	} `json:"key_schedule"`
	HelloRetry struct {
		FirstHelloTranscript Hex `json:"first_hello_transcript"`
		RetryTranscript      Hex `json:"retry_transcript"`
		RetrySignature       Hex `json:"retry_signature"`
	} `json:"hello_retry"`
	EnrollmentProof struct {
		Signed
		ClaimDigest Hex `json:"claim_digest"`
	} `json:"enrollment_proof"`
	KeyRotation struct {
		Transcript      Hex `json:"transcript"`
		SignatureOldKey Hex `json:"signature_old_key"`
		SignatureNewKey Hex `json:"signature_new_key"`
	} `json:"key_rotation"`
	CredentialRenewal Signed `json:"credential_renewal"`
}

// LoadVectors reads the vectors file.
func LoadVectors(t testing.TB) *Vectors {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "testdata", "edge-link-handshake-v1.vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v Vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

// PlatformKey is the vectors' platform key certificate.
func (v *Vectors) PlatformKey() *edgelinkv3.PlatformKey {
	return &edgelinkv3.PlatformKey{
		InstallationId: v.Inputs.InstallationID,
		AnchorKeyId:    v.Keys.AnchorKeyID,
		KeyId:          v.Keys.PlatformHandshakeKeyID,
		KeyAlg:         2,
		PublicKey:      v.Keys.PlatformHandshakePublic,
		NotBefore:      v.PlatformKeyCertificate.NotBefore,
		NotAfter:       v.PlatformKeyCertificate.NotAfter,
		Signature:      v.PlatformKeyCertificate.Signature,
	}
}

// WelcomeFrame is the vectors' signed Welcome.
func (v *Vectors) WelcomeFrame() *edgelinkv3.Welcome {
	return &edgelinkv3.Welcome{
		EphemeralPub:   v.Keys.PlatformEphemeralPublic,
		ClockOffsetMs:  -1500,
		ResumeFrom:     &edgelinkv3.Ack{Epoch: 3, CumulativeSeq: 1200},
		SessionId:      "5d2f1e8a-9b47-4c30-a6d1-0e7f3b2c9a84",
		Policy:         &edgelinkv3.LinkPolicy{Revision: "PT15S", PingInterval: durationpb.New(15e9)},
		PlatformKey:    v.PlatformKey(),
		ServedAssetSns: []string{"SIM-DRONE-01"},
		Signature:      v.Welcome.Signature,
	}
}

// HelloRetryFrame is the vectors' signed HelloRetry.
func (v *Vectors) HelloRetryFrame() *edgelinkv3.HelloRetry {
	return &edgelinkv3.HelloRetry{
		ServerNonce:  v.Inputs.ServerNonce,
		PlatformTime: &timestamppb.Timestamp{Seconds: 1793534460},
		Reason:       edgelinkv3.HelloRetryReason_HELLO_RETRY_REASON_CLOCK_UNTRUSTED,
		PlatformKey:  v.PlatformKey(),
		Signature:    v.HelloRetry.RetrySignature,
	}
}
