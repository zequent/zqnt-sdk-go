package identity

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
)

// ProvisioningQRPrefix starts a provisioning payload carried in a QR code: the prefix followed by
// the provisioning JSON in unpadded base64url.
const ProvisioningQRPrefix = "ZQNT-PROV1:"

// Provisioning pins the platform before enrollment (air-gapped installs, phones, installations
// that avoid trust on first use): the installation, its anchor keys and where to enroll.
//
//	{
//	  "version": 1,
//	  "installation_id": "inst-7f3a",
//	  "anchors": [{"key_id": "a9c6451c6ffc4d2ccf7043e60e4d3cee", "alg": 2, "public_key": "BH…"}],
//	  "platform": {"endpoint": "edge-link.example.com:443", "server_name": "", "ca_pem": "-----BEGIN…"},
//	  "claim_code": "ZQ-ABCDE-FGHJK"
//	}
//
// An anchor may carry only its key id (the fingerprint the console shows); the enrollment response
// must then name exactly these anchors.
type Provisioning struct {
	Version        int                  `json:"version"`
	InstallationID string               `json:"installation_id"`
	Anchors        []Anchor             `json:"anchors"`
	Platform       ProvisioningPlatform `json:"platform"`
	ClaimCode      string               `json:"claim_code,omitempty"`
}

// ProvisioningPlatform is the enrollment endpoint of a provisioning file.
type ProvisioningPlatform struct {
	Endpoint   string `json:"endpoint,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	CAPEM      string `json:"ca_pem,omitempty"`
}

// ReadProvisioning reads a provisioning file.
func ReadProvisioning(path string) (*Provisioning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("identity: provisioning: %w", err)
	}
	return ParseProvisioning(data)
}

// ParseProvisioningQR reads the payload of a provisioning QR code.
func ParseProvisioningQR(payload string) (*Provisioning, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(payload), ProvisioningQRPrefix)
	if !ok {
		return nil, fmt.Errorf("identity: provisioning QR payload must start with %s", ProvisioningQRPrefix)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(rest, "="))
	if err != nil {
		return nil, fmt.Errorf("identity: provisioning QR payload: %w", err)
	}
	return ParseProvisioning(data)
}

// EncodeQR is the QR payload of p.
func (p *Provisioning) EncodeQR() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return ProvisioningQRPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// ParseProvisioning reads and checks provisioning JSON.
func ParseProvisioning(data []byte) (*Provisioning, error) {
	var p Provisioning
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("identity: provisioning: %w", err)
	}
	var errs []error
	if p.Version != 1 {
		errs = append(errs, fmt.Errorf("version %d, want 1", p.Version))
	}
	if p.InstallationID == "" {
		errs = append(errs, errors.New("installation_id is required"))
	}
	if len(p.Anchors) == 0 {
		errs = append(errs, errors.New("at least one anchor is required"))
	}
	for i, a := range p.Anchors {
		switch {
		case len(a.KeyID) != handshake.KeyIDSize:
			errs = append(errs, fmt.Errorf("anchors[%d]: key_id must be 16 bytes of hex", i))
		case a.Alg != uint32(handshake.AlgECDSAP256):
			errs = append(errs, fmt.Errorf("anchors[%d]: alg %d, want 2 (ECDSA P-256)", i, a.Alg))
		case len(a.PublicKey) > 0 && !bytes.Equal(a.KeyID, handshake.KeyID(handshake.AlgECDSAP256, a.PublicKey)):
			errs = append(errs, fmt.Errorf("anchors[%d]: public_key does not match key_id", i))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("identity: provisioning: %w", errors.Join(errs...))
	}
	return &p, nil
}

// matchAnchors checks that the enrollment response names exactly the provisioned anchors.
func (p *Provisioning) matchAnchors(installationID string, got []Anchor) error {
	if installationID != p.InstallationID {
		return fmt.Errorf("installation %q, provisioned %q", installationID, p.InstallationID)
	}
	if len(got) != len(p.Anchors) {
		return fmt.Errorf("%d anchors, provisioned %d", len(got), len(p.Anchors))
	}
	for _, want := range p.Anchors {
		found := false
		for _, a := range got {
			if bytes.Equal(a.KeyID, want.KeyID) && (len(want.PublicKey) == 0 || bytes.Equal(a.PublicKey, want.PublicKey)) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("provisioned anchor %x is missing", []byte(want.KeyID))
		}
	}
	return nil
}
