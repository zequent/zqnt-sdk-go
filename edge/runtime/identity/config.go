package identity

import (
	"errors"
	"fmt"
	"os"
	"strings"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
)

// Defaults of the identity configuration.
const (
	DefaultDir = "/var/lib/zqnt-edge-node/identity"
)

// Key store backends.
const (
	BackendTPM               = "tpm"
	BackendSoftwareTPMSealed = "software-tpm-sealed"
	BackendSoftwareDev       = "software-dev"
)

// Node classes as written in configuration.
const (
	ClassInfrastructure = "infrastructure"
	ClassPersonal       = "personal"
)

// Config is the identity section of the node configuration.
//
//	identity:
//	  key_store: tpm                 # tpm | software-tpm-sealed | software-dev
//	  dir: /var/lib/zqnt-edge-node/identity
//	  tpm_device: /dev/tpmrm0        # tpm and software-tpm-sealed
//	  sealing_key_file: dev.key      # software-dev only, optional: encrypt the dev key at rest
//	  node_class: infrastructure     # infrastructure | personal
//	  suite: s1-chacha20-poly1305    # or s1-aes-256-gcm
//	  dev: false                     # software-dev keys are refused unless true
type Config struct {
	KeyStore       string `yaml:"key_store"`
	Dir            string `yaml:"dir"`
	TPMDevice      string `yaml:"tpm_device"`
	SealingKeyFile string `yaml:"sealing_key_file"`
	SealingKeyEnv  string `yaml:"sealing_key_env"`
	NodeClass      string `yaml:"node_class"`
	Suite          string `yaml:"suite"`
	Dev            bool   `yaml:"dev"`
}

// ApplyDefaults fills in the directory, the TPM device, the node class and the suite.
func (c *Config) ApplyDefaults() {
	if c.Dir == "" {
		c.Dir = DefaultDir
	}
	if c.usesTPM() && c.TPMDevice == "" {
		c.TPMDevice = DefaultTPMDevice
	}
	if c.NodeClass == "" {
		c.NodeClass = ClassInfrastructure
	}
	if c.Suite == "" {
		c.Suite = suiteNames[uint32(edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_CHACHA20_POLY1305)]
	}
}

var suiteNames = map[uint32]string{
	uint32(edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_CHACHA20_POLY1305): "s1-chacha20-poly1305",
	uint32(edgelinkv3.CryptoSuite_CRYPTO_SUITE_S1_AES_256_GCM):       "s1-aes-256-gcm",
}

func (c Config) usesTPM() bool {
	return c.KeyStore == BackendTPM || c.KeyStore == BackendSoftwareTPMSealed
}

// SuiteID is the configured crypto suite.
func (c Config) SuiteID() (uint32, error) {
	for id, name := range suiteNames {
		if strings.EqualFold(c.Suite, name) {
			return id, nil
		}
	}
	return 0, fmt.Errorf("suite %q: want s1-chacha20-poly1305 or s1-aes-256-gcm", c.Suite)
}

// Class is the configured node class.
func (c Config) Class() (edgelinkv3.NodeClass, error) {
	switch strings.ToLower(c.NodeClass) {
	case ClassInfrastructure:
		return edgelinkv3.NodeClass_NODE_CLASS_INFRASTRUCTURE, nil
	case ClassPersonal:
		return edgelinkv3.NodeClass_NODE_CLASS_PERSONAL, nil
	}
	return 0, fmt.Errorf("node_class %q: want infrastructure or personal", c.NodeClass)
}

// Validate checks the section without opening the key store; every problem is reported.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf("identity."+format, args...)) }
	switch c.KeyStore {
	case "":
		add("key_store is required (tpm, software-tpm-sealed or software-dev)")
	case BackendTPM, BackendSoftwareTPMSealed:
	case BackendSoftwareDev:
		if !c.Dev {
			add("key_store software-dev keeps the identity key in software without hardware protection; it is refused unless identity.dev is true (development and simulators only)")
		}
		if c.SealingKeyFile != "" && c.SealingKeyEnv != "" {
			add("sealing_key_file and sealing_key_env: set one")
		}
	default:
		add("key_store %q: want tpm, software-tpm-sealed or software-dev", c.KeyStore)
	}
	if (c.SealingKeyFile != "" || c.SealingKeyEnv != "") && c.KeyStore != BackendSoftwareDev {
		add("sealing_key_file/sealing_key_env: only for key_store software-dev")
	}
	if c.TPMDevice != "" && !c.usesTPM() {
		add("tpm_device: only for key_store tpm and software-tpm-sealed")
	}
	if _, err := c.SuiteID(); err != nil {
		add("%v", err)
	}
	if _, err := c.Class(); err != nil {
		add("%v", err)
	}
	return errors.Join(errs...)
}

// Check validates the section and checks that the key store's secret or device is reachable,
// without changing anything.
func (c Config) Check() error {
	if err := c.Validate(); err != nil {
		return err
	}
	switch {
	case c.SealingKeyFile != "" || c.SealingKeyEnv != "":
		key, err := c.sealingKey()
		clear(key)
		return err
	case c.usesTPM():
		if _, err := os.Stat(c.TPMDevice); err != nil {
			return fmt.Errorf("identity.tpm_device: %w", err)
		}
	}
	return nil
}

func (c Config) sealingKey() ([]byte, error) {
	if c.SealingKeyFile != "" {
		return ReadSealingKeyFile(c.SealingKeyFile)
	}
	v, ok := os.LookupEnv(c.SealingKeyEnv)
	if !ok || v == "" {
		return nil, fmt.Errorf("identity: sealing key: environment variable %s is not set", c.SealingKeyEnv)
	}
	return ParseSealingKey([]byte(v))
}

// OpenKeyStore opens the configured key store. A key store that is not available is an error:
// the node never falls back to another one.
func (c Config) OpenKeyStore() (KeyStore, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	switch {
	case c.KeyStore == BackendTPM:
		return OpenTPMKeyStore(c.Dir, c.TPMDevice)
	case c.KeyStore == BackendSoftwareTPMSealed:
		return OpenTPMSealedKeyStore(c.Dir, c.TPMDevice)
	case c.SealingKeyFile != "" || c.SealingKeyEnv != "":
		key, err := c.sealingKey()
		if err != nil {
			return nil, err
		}
		defer clear(key)
		return NewFileSealedKeyStore(c.Dir, key)
	default:
		return NewDevKeyStore(c.Dir)
	}
}
