// Package config says where the platform is and which credential to call it with: the local
// development stack when nothing is set, and a deployment's environment variables otherwise.
//
// The variables are the ones every Zequent client SDK (Java, Python, Go) reads, so one .env works
// for every language:
//
//	CONNECTOR_SERVICE_HOST / _PORT / _USE_PLAINTEXT          localhost / 8010 / true
//	REMOTE_CONTROL_SERVICE_HOST / _PORT / _USE_PLAINTEXT     localhost / 8002 / true
//	LIVE_DATA_SERVICE_HOST / _PORT / _USE_PLAINTEXT          localhost / 8003 / true
//	MISSION_AUTONOMY_SERVICE_HOST / _PORT / _USE_PLAINTEXT   localhost / 8004 / true
//	ZQNT_CLIENT_TOKEN                                        none
//	ZQNT_INTERNAL_TLS / ZQNT_INTERNAL_TLS_CA                 false / /etc/zqnt/internal-tls/ca.crt
//
// Nothing set is the local stack (quarkus:dev or docker-compose.local.yml). A deployment sets the
// hosts, _USE_PLAINTEXT=false for TLS (system trust store) whenever traffic leaves a private
// network, and ZQNT_CLIENT_TOKEN from its secret store. A client running inside an installation
// with internal TLS sets ZQNT_INTERNAL_TLS=true: every service is then dialled over TLS, verified
// against the installation CA in ZQNT_INTERNAL_TLS_CA (a service's own _USE_PLAINTEXT still wins
// for that service). There is deliberately no built-in development credential: the local platform
// refuses anonymous calls too.
//
//	cfg, err := config.FromEnv()
//	if err != nil { ... }
//	conn, err := cfg.Dial(cfg.Connector)
//	assets := connector.New(conn)
package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/zequent/zqnt-sdk-go/v3/client/auth"
	"github.com/zequent/zqnt-sdk-go/v3/internaltls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Environment variable prefixes, one per platform service.
const (
	ConnectorPrefix       = "CONNECTOR_SERVICE"
	RemoteControlPrefix   = "REMOTE_CONTROL_SERVICE"
	LiveDataPrefix        = "LIVE_DATA_SERVICE"
	MissionAutonomyPrefix = "MISSION_AUTONOMY_SERVICE"
)

// Local ports of the platform's services (quarkus:dev, docker-compose.local.yml).
const (
	LocalHost                = "localhost"
	LocalConnectorPort       = 8010
	LocalRemoteControlPort   = 8002
	LocalLiveDataPort        = 8003
	LocalMissionAutonomyPort = 8004
)

// Endpoint is where one platform service listens.
type Endpoint struct {
	Host string
	Port int
	// Plaintext: no TLS. Fine on localhost or inside a private network; use TLS whenever the
	// credential crosses a network you do not control.
	Plaintext bool
}

// Target is the address to dial.
func (e Endpoint) Target() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// Config is the platform's four services and the client credential. The credential is kept
// unexported so that printing a Config never prints it.
type Config struct {
	Connector       Endpoint
	RemoteControl   Endpoint
	LiveData        Endpoint
	MissionAutonomy Endpoint
	// InternalTLS: on, a TLS endpoint is verified against the installation's CA instead of the
	// system trust store (ZQNT_INTERNAL_TLS, ZQNT_INTERNAL_TLS_CA).
	InternalTLS internaltls.Config
	token       string
}

// Local is the local development stack, with the credential from ZQNT_CLIENT_TOKEN if set.
func Local() Config {
	return Config{
		Connector:       Endpoint{Host: LocalHost, Port: LocalConnectorPort, Plaintext: true},
		RemoteControl:   Endpoint{Host: LocalHost, Port: LocalRemoteControlPort, Plaintext: true},
		LiveData:        Endpoint{Host: LocalHost, Port: LocalLiveDataPort, Plaintext: true},
		MissionAutonomy: Endpoint{Host: LocalHost, Port: LocalMissionAutonomyPort, Plaintext: true},
		token:           auth.Token(""),
	}
}

// FromEnv is Local, overridden by whatever the environment sets.
func FromEnv() (Config, error) {
	return FromLookup(os.LookupEnv)
}

// FromLookup is FromEnv against another source of variables (tests, a loaded .env file).
func FromLookup(lookup func(string) (string, bool)) (Config, error) {
	c := Local()
	c.token = ""
	c.InternalTLS = internaltls.FromLookup(lookup)
	var err error
	for _, s := range []struct {
		prefix   string
		endpoint *Endpoint
	}{
		{ConnectorPrefix, &c.Connector},
		{RemoteControlPrefix, &c.RemoteControl},
		{LiveDataPrefix, &c.LiveData},
		{MissionAutonomyPrefix, &c.MissionAutonomy},
	} {
		local := *s.endpoint
		local.Plaintext = !c.InternalTLS.Enabled
		if *s.endpoint, err = endpoint(lookup, s.prefix, local); err != nil {
			return Config{}, err
		}
	}
	c.token = value(lookup, auth.EnvVar)
	return c, nil
}

// WithToken returns c with an explicit client credential (it wins over ZQNT_CLIENT_TOKEN).
func (c Config) WithToken(token string) Config {
	c.token = strings.TrimSpace(token)
	return c
}

// HasToken reports whether a client credential is configured (it is never exposed).
func (c Config) HasToken() bool {
	return c.token != ""
}

// DialOptions are the options to dial e with: its transport (see TransportCredentials), then
// extra — the host application's own options, e.g. interceptors that set a per-caller credential,
// which run before the SDK's — then the client credential (auth.DialOptions). A CA that cannot be
// read is not an option error: every connection attempt then fails with the explanation (Dial
// reports it up front).
func (c Config) DialOptions(e Endpoint, extra ...grpc.DialOption) []grpc.DialOption {
	transport, err := c.TransportCredentials(e)
	if err != nil {
		transport = failingCredentials{err: err}
	}
	opts := append([]grpc.DialOption{grpc.WithTransportCredentials(transport)}, extra...)
	return append(opts, auth.DialOptions(c.token)...)
}

// TransportCredentials are e's transport: plaintext when e.Plaintext, else TLS verified against
// the installation CA when InternalTLS is on, or the system trust store otherwise.
func (c Config) TransportCredentials(e Endpoint) (credentials.TransportCredentials, error) {
	if e.Plaintext {
		return insecure.NewCredentials(), nil
	}
	if c.InternalTLS.Enabled {
		return c.InternalTLS.TransportCredentials()
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}), nil
}

// Dial creates a client connection to e (see DialOptions). grpc.NewClient connects lazily; the
// first call reports an unreachable service. An unreadable installation CA is reported here.
func (c Config) Dial(e Endpoint, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	if _, err := c.TransportCredentials(e); err != nil {
		return nil, err
	}
	return grpc.NewClient(e.Target(), c.DialOptions(e, extra...)...)
}

// failingCredentials stand in for a TLS transport whose CA could not be read: every handshake
// fails with that error instead of the connection silently going out in plaintext.
type failingCredentials struct {
	err error
}

func (f failingCredentials) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, f.err
}

func (f failingCredentials) ServerHandshake(net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, f.err
}

func (failingCredentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "tls"}
}

func (f failingCredentials) Clone() credentials.TransportCredentials {
	return f
}

func (failingCredentials) OverrideServerName(string) error {
	return nil
}

func endpoint(lookup func(string) (string, bool), prefix string, local Endpoint) (Endpoint, error) {
	e := local
	if host := value(lookup, prefix+"_HOST"); host != "" {
		e.Host = host
	}
	if raw := value(lookup, prefix+"_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return Endpoint{}, fmt.Errorf("%s_PORT must be a port number (1-65535), was %q", prefix, raw)
		}
		e.Port = port
	}
	if raw := value(lookup, prefix+"_USE_PLAINTEXT"); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "on":
			e.Plaintext = true
		default:
			e.Plaintext = false
		}
	}
	return e, nil
}

func value(lookup func(string) (string, bool), name string) string {
	v, _ := lookup(name)
	return strings.TrimSpace(v)
}

// String describes c without the credential.
func (c Config) String() string {
	return fmt.Sprintf("config.Config{Connector:%s RemoteControl:%s LiveData:%s MissionAutonomy:%s %s Token:%s}",
		c.Connector, c.RemoteControl, c.LiveData, c.MissionAutonomy, c.InternalTLS, redacted(c.token))
}

// GoString is String, so %#v does not print the credential either.
func (c Config) GoString() string {
	return c.String()
}

// String is host:port and the transport.
func (e Endpoint) String() string {
	if e.Plaintext {
		return e.Target() + " (plaintext)"
	}
	return e.Target() + " (tls)"
}

func redacted(token string) string {
	if token == "" {
		return "<none>"
	}
	return "<set>"
}
