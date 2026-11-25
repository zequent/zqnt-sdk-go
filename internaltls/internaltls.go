// Package internaltls is the one switch that moves an adapter's or a client's connections into
// the platform onto the installation's internal TLS (zqnt-core #371): ZQNT_INTERNAL_TLS=true and
// the installation CA in ZQNT_INTERNAL_TLS_CA (default /etc/zqnt/internal-tls/ca.crt). On, every
// connection to a core service verifies the server's certificate against that CA and the dialled
// host name; the HTTP client that fetches the platform's key set trusts the CA next to the system
// roots. Off (the default) nothing changes: plaintext gRPC, plain HTTP. No client certificate --
// the edge credential or client token keeps authenticating the caller.
package internaltls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// EnabledVar switches internal TLS on (true/1/yes/on).
	EnabledVar = "ZQNT_INTERNAL_TLS"
	// CAFileVar names the installation CA in PEM.
	CAFileVar = "ZQNT_INTERNAL_TLS_CA"
	// DefaultCAFile is where the installation mounts the CA.
	DefaultCAFile = "/etc/zqnt/internal-tls/ca.crt"
)

// Config says whether connections into the platform use the installation's internal TLS, and
// which CA they verify it against.
type Config struct {
	Enabled bool
	CAFile  string
}

// FromEnv reads ZQNT_INTERNAL_TLS and ZQNT_INTERNAL_TLS_CA.
func FromEnv() Config {
	return FromLookup(os.LookupEnv)
}

// FromLookup is FromEnv against another source of variables.
func FromLookup(lookup func(string) (string, bool)) Config {
	enabled, _ := lookup(EnabledVar)
	caFile, _ := lookup(CAFileVar)
	cfg := Config{CAFile: strings.TrimSpace(caFile)}
	if cfg.CAFile == "" {
		cfg.CAFile = DefaultCAFile
	}
	switch strings.ToLower(strings.TrimSpace(enabled)) {
	case "1", "true", "yes", "on":
		cfg.Enabled = true
	}
	return cfg
}

// CertPool is the installation CA alone, for the gRPC connections into the core services. Nil
// when internal TLS is off.
func (c Config) CertPool() (*x509.CertPool, error) {
	if !c.Enabled {
		return nil, nil
	}
	pool := x509.NewCertPool()
	if err := c.appendCA(pool); err != nil {
		return nil, err
	}
	return pool, nil
}

// TLSConfig verifies a core service, Redis or PostgreSQL against the installation CA and the
// dialled host name (go-redis: Options.TLSConfig). Nil when internal TLS is off.
func (c Config) TLSConfig() (*tls.Config, error) {
	pool, err := c.CertPool()
	if err != nil || pool == nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, nil
}

// TransportCredentials are the gRPC transport credentials for a connection into the platform:
// TLS against the installation CA when on, plaintext when off.
func (c Config) TransportCredentials() (credentials.TransportCredentials, error) {
	cfg, err := c.TLSConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return insecure.NewCredentials(), nil
	}
	return credentials.NewTLS(cfg), nil
}

// HTTPClient reaches the platform's HTTP endpoints (the key set) over https with the installation
// CA trusted next to the system roots, so the same client still reaches a public URL. A plain
// client when internal TLS is off.
func (c Config) HTTPClient(timeout time.Duration) (*http.Client, error) {
	if !c.Enabled {
		return &http.Client{Timeout: timeout}, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if err := c.appendCA(pool); err != nil {
		return nil, err
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("internaltls: http.DefaultTransport is not an *http.Transport")
	}
	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

func (c Config) appendCA(pool *x509.CertPool) error {
	pemBytes, err := os.ReadFile(c.CAFile)
	if err != nil {
		return fmt.Errorf("internaltls: %s is on but the CA cannot be read from %s (%s): %w", EnabledVar, c.CAFile, CAFileVar, err)
	}
	if !pool.AppendCertsFromPEM(pemBytes) {
		return fmt.Errorf("internaltls: %s (%s) holds no PEM certificate", c.CAFile, CAFileVar)
	}
	return nil
}

// String describes c.
func (c Config) String() string {
	if !c.Enabled {
		return "internal TLS off"
	}
	return "internal TLS on (CA " + c.CAFile + ")"
}
