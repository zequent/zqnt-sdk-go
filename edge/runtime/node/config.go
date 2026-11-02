package node

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"go.yaml.in/yaml/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// Defaults of the node configuration.
const (
	DefaultStorePath       = "/var/lib/zqnt-edge-node/node.db"
	DefaultSocketPath      = "/run/zqnt-edge-node/node.sock"
	DefaultReportInterval  = time.Minute
	DefaultShutdownTimeout = 10 * time.Second
)

// Config is the node configuration file. The node id is not configured: the platform assigns it
// at enrollment (zqnt-edge-node enroll) and the node reads it from the identity directory.
//
//	identity:
//	  key_store: tpm
//	assets: [DOCK-1]
//	store:
//	  path: /var/lib/zqnt-edge-node/node.db
//	quotas:
//	  telemetry: {max_bytes: 20MiB}
//	local:
//	  socket: /run/zqnt-edge-node/node.sock
//	  mode: "0660"
//	  group: zqnt
//	  default_adapter_endpoint: unix:///run/zqnt-adapter/adapter.sock
//	report_interval: 1m
//	links:
//	  - id: lte
//	    driver: ip-session
//	    endpoint: edge-link.example.com:443
type Config struct {
	// Identity is the node identity: key store, identity directory, node class and suite.
	Identity identity.Config `yaml:"identity"`
	// Assets are announced in every Hello, together with the assets adapters register on the
	// local socket while the node runs.
	Assets []string    `yaml:"assets"`
	Store  StoreConfig `yaml:"store"`
	// Quotas override the default lane quotas, keyed by lane: alarm, command_reply, status,
	// telemetry, bulk.
	Quotas map[string]Quota `yaml:"quotas"`
	Local  LocalConfig      `yaml:"local"`
	// ReportInterval is how often a LinkReport goes to the platform and the node logs its stats.
	ReportInterval Duration `yaml:"report_interval"`
	// ShutdownTimeout bounds how long a stopping node waits for running commands.
	ShutdownTimeout Duration  `yaml:"shutdown_timeout"`
	Log             LogConfig `yaml:"log"`
	link.Config     `yaml:",inline"`
}

// StoreConfig places the node's durable outbox.
type StoreConfig struct {
	Path string `yaml:"path"`
	// FlushInterval is the longest an append waits for its batch commit (200ms).
	FlushInterval Duration `yaml:"flush_interval"`
}

// Quota is the limit of one lane; zero keeps the default.
type Quota struct {
	MaxItems int      `yaml:"max_items"`
	MaxBytes ByteSize `yaml:"max_bytes"`
}

// LocalConfig is the socket Device Adapters attach to.
type LocalConfig struct {
	Socket string `yaml:"socket"`
	// Mode is the socket's permission bits, written in octal ("0600", "0660").
	Mode FileMode `yaml:"mode"`
	// Group, a name or a numeric id, becomes the socket's group.
	Group string `yaml:"group"`
	// DefaultAdapterEndpoint receives commands for assets whose adapter did not name its socket.
	DefaultAdapterEndpoint string `yaml:"default_adapter_endpoint"`
}

// LogConfig sets the node's log output.
type LogConfig struct {
	// Level is debug, info, warn or error (info).
	Level string `yaml:"level"`
	// Format is text or json (text).
	Format string `yaml:"format"`
}

// Duration is a Go duration written as text ("500ms", "1m").
type Duration time.Duration

// UnmarshalYAML reads a duration such as "30s".
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

// ByteSize is a size in bytes, written as a number or with a unit (KB, MB, GB, KiB, MiB, GiB).
type ByteSize int64

var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
	{"B", 1},
}

// UnmarshalYAML reads a size such as 1048576 or "50MiB".
func (b *ByteSize) UnmarshalYAML(n *yaml.Node) error {
	s := strings.TrimSpace(n.Value)
	factor := int64(1)
	for _, u := range byteUnits {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			s, factor = strings.TrimSpace(rest), u.factor
			break
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 || v > (1<<62)/factor {
		return fmt.Errorf("line %d: %q is not a size (e.g. 1048576, 50MiB)", n.Line, n.Value)
	}
	*b = ByteSize(v * factor)
	return nil
}

// FileMode is permission bits written in octal, quoted or not.
type FileMode fs.FileMode

// UnmarshalYAML reads a mode such as "0660".
func (m *FileMode) UnmarshalYAML(n *yaml.Node) error {
	v, err := strconv.ParseUint(strings.TrimPrefix(n.Value, "0o"), 8, 32)
	if err != nil {
		return fmt.Errorf("line %d: mode %q is not octal (e.g. \"0660\")", n.Line, n.Value)
	}
	*m = FileMode(v)
	return nil
}

// LoadConfig reads, defaults and validates the configuration file at path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("node: config: %w", err)
	}
	return ParseConfig(data)
}

// ParseConfig reads, defaults and validates a configuration. Unknown keys are errors.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("node: config: %w", err)
	}
	cfg.applyDefaults()
	return cfg, cfg.Validate()
}

func (c *Config) applyDefaults() {
	c.Identity.ApplyDefaults()
	if c.Store.Path == "" {
		c.Store.Path = DefaultStorePath
	}
	if c.Local.Socket == "" {
		c.Local.Socket = DefaultSocketPath
	}
	if c.Local.Mode == 0 {
		c.Local.Mode = FileMode(local.DefaultSocketMode)
	}
	if c.ReportInterval == 0 {
		c.ReportInterval = Duration(DefaultReportInterval)
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = Duration(DefaultShutdownTimeout)
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
}

// Validate checks the whole configuration and reports every problem at once.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if err := c.Identity.Validate(); err != nil {
		errs = append(errs, err)
	}
	seen := map[string]bool{}
	for i, sn := range c.Assets {
		switch {
		case strings.TrimSpace(sn) == "":
			add("assets[%d]: empty serial number", i)
		case seen[sn]:
			add("assets[%d]: duplicate %q", i, sn)
		}
		seen[sn] = true
	}
	if c.Store.FlushInterval < 0 {
		add("store.flush_interval must not be negative")
	}
	for name, q := range c.Quotas {
		if _, err := ParseLane(name); err != nil {
			add("quotas: %v", err)
		}
		if q.MaxItems < 0 || q.MaxBytes < 0 {
			add("quotas.%s: limits must not be negative", name)
		}
	}
	mode := fs.FileMode(c.Local.Mode)
	if mode&0o007 != 0 || mode&^fs.ModePerm != 0 {
		add("local.mode %#o: only owner and group may have access", mode)
	}
	if c.Local.Group != "" {
		if _, err := lookupGroup(c.Local.Group); err != nil {
			add("local.group: %v", err)
		}
	}
	if ep := c.Local.DefaultAdapterEndpoint; ep != "" && !isUnixEndpoint(ep) {
		add("local.default_adapter_endpoint %q is not an absolute unix socket (unix:///path)", ep)
	}
	if c.ReportInterval < Duration(time.Second) {
		add("report_interval must be at least 1s")
	}
	if c.ShutdownTimeout < 0 {
		add("shutdown_timeout must not be negative")
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		add("log.level %q: want debug, info, warn or error", c.Log.Level)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		add("log.format %q: want text or json", c.Log.Format)
	}
	switch len(c.Links) {
	case 0:
		add("links: one link is required")
	case 1:
	default:
		add("links: %d configured, but this node runs one link until the link manager lands", len(c.Links))
	}
	if c.Relay.Enabled {
		add("relay: relaying is not available in this node yet")
	}
	if err := c.Config.Validate(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("node: config: %w", errors.Join(errs...))
	}
	return nil
}

// ParseLane reads a lane name as written in quotas ("alarm", "command_reply", "LANE_BULK").
func ParseLane(name string) (store.Lane, error) {
	key := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if !strings.HasPrefix(key, "LANE_") {
		key = "LANE_" + key
	}
	v, ok := edgelinkv3.Lane_value[key]
	if !ok || v == int32(edgelinkv3.Lane_LANE_UNSPECIFIED) {
		return 0, fmt.Errorf("unknown lane %q (alarm, command_reply, status, telemetry, bulk)", name)
	}
	return store.Lane(v), nil
}

// Lanes is the store's lane policy: the defaults with the configured quotas applied.
func (c Config) Lanes() map[store.Lane]store.LanePolicy {
	lanes := store.DefaultLanes()
	for name, q := range c.Quotas {
		lane, err := ParseLane(name)
		if err != nil {
			continue
		}
		pol := lanes[lane]
		if q.MaxItems > 0 {
			pol.MaxItems = q.MaxItems
		}
		if q.MaxBytes > 0 {
			pol.MaxBytes = int64(q.MaxBytes)
		}
		lanes[lane] = pol
	}
	return lanes
}

func isUnixEndpoint(ep string) bool {
	path, ok := strings.CutPrefix(ep, "unix://")
	if !ok {
		path, ok = strings.CutPrefix(ep, "unix:")
	}
	return ok && strings.HasPrefix(path, "/")
}

func lookupGroup(group string) (int, error) {
	if gid, err := strconv.Atoi(group); err == nil {
		if gid < 0 {
			return 0, fmt.Errorf("group id %d is negative", gid)
		}
		return gid, nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}
