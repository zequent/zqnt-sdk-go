package node_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	_ "github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/ipsession"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/node"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

const minimal = `
node_id: pi-1
links:
  - id: lte
    driver: ip-session
    endpoint: edge-link.example.com:443
`

func TestAMinimalConfigTakesTheDefaults(t *testing.T) {
	cfg, err := node.ParseConfig([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.Path != node.DefaultStorePath || cfg.Local.Socket != node.DefaultSocketPath {
		t.Fatalf("store %q socket %q", cfg.Store.Path, cfg.Local.Socket)
	}
	if cfg.Local.Mode != 0o600 || time.Duration(cfg.ReportInterval) != time.Minute ||
		time.Duration(cfg.ShutdownTimeout) != 10*time.Second || cfg.Log.Level != "info" || cfg.Log.Format != "text" {
		t.Fatalf("defaults: %+v", cfg)
	}
	if got := cfg.Links[0].Options["endpoint"]; got != "edge-link.example.com:443" {
		t.Fatalf("link options %v", cfg.Links[0].Options)
	}
	if got := cfg.Lanes(); got[edgelinkv3.Lane_LANE_TELEMETRY] != store.DefaultLanes()[edgelinkv3.Lane_LANE_TELEMETRY] {
		t.Fatalf("lanes %v", got)
	}
}

func TestTheExampleConfigIsValid(t *testing.T) {
	if _, err := node.LoadConfig(filepath.Join("..", "..", "..", "examples", "edge-node", "node.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestAFullConfigIsRead(t *testing.T) {
	cfg, err := node.ParseConfig([]byte(`
node_id: pi-1
assets: [DOCK-1, DRONE-1]
store:
  path: /data/node.db
  flush_interval: 50ms
quotas:
  telemetry: {max_bytes: 20MiB}
  alarm: {max_items: 500}
  command-reply: {max_bytes: 1000000}
local:
  socket: /run/n/node.sock
  mode: 0660
  group: "0"
  default_adapter_endpoint: unix:///run/n/adapter.sock
report_interval: 30s
shutdown_timeout: 2s
log: {level: debug, format: json}
links:
  - id: lte
    driver: ip-session
    endpoint: edge-link.example.com:443
    token_file: /etc/zqnt/token
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Local.Mode != 0o660 || cfg.Local.Group != "0" || time.Duration(cfg.Store.FlushInterval) != 50*time.Millisecond {
		t.Fatalf("local %+v store %+v", cfg.Local, cfg.Store)
	}
	lanes := cfg.Lanes()
	if lanes[edgelinkv3.Lane_LANE_TELEMETRY].MaxBytes != 20<<20 || lanes[edgelinkv3.Lane_LANE_TELEMETRY].Overflow != store.Thin {
		t.Fatalf("telemetry %+v", lanes[edgelinkv3.Lane_LANE_TELEMETRY])
	}
	if lanes[edgelinkv3.Lane_LANE_ALARM].MaxItems != 500 || !lanes[edgelinkv3.Lane_LANE_ALARM].Sync {
		t.Fatalf("alarm %+v", lanes[edgelinkv3.Lane_LANE_ALARM])
	}
	if reply := lanes[edgelinkv3.Lane_LANE_COMMAND_REPLY]; reply.MaxBytes != 1000000 || reply.MaxItems != 2000 {
		t.Fatalf("command reply %+v", reply)
	}
}

func TestInvalidConfigsNameEveryProblem(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want []string
	}{
		"empty":       {"{}", []string{"node_id is required", "links: one link is required"}},
		"unknown key": {minimal + "report_intervall: 1m\n", []string{"field report_intervall not found"}},
		"bad values": {`
node_id: "pi 1"
assets: [A, A, ""]
quotas: {radio: {max_items: 1}, bulk: {max_items: -1}}
local:
  mode: "0666"
  group: no-such-group-zqnt
  default_adapter_endpoint: localhost:9090
report_interval: 10ms
log: {level: loud, format: xml}
links:
  - id: lte
    driver: ip-session
    endpoint: x:1
`, []string{
			`node_id "pi 1"`, `assets[1]: duplicate "A"`, "assets[2]: empty serial number",
			`unknown lane "radio"`, "quotas.bulk: limits must not be negative",
			"local.mode 0666", "local.group", "local.default_adapter_endpoint",
			"report_interval must be at least 1s", `log.level "loud"`, `log.format "xml"`,
		}},
		"bad mode":     {minimal + "local: {mode: rw}\n", []string{`mode "rw" is not octal`}},
		"bad size":     {minimal + "quotas: {bulk: {max_bytes: lots}}\n", []string{`"lots" is not a size`}},
		"bad duration": {minimal + "report_interval: soon\n", []string{"soon"}},
		"identity":     {minimal + "identity: {key_file: /etc/key}\n", []string{"identity: node identity and enrollment are not available yet"}},
		"two links": {`
node_id: pi-1
links:
  - {id: lte, driver: ip-session, endpoint: "a:1"}
  - {id: wifi, driver: ip-session, endpoint: "b:1"}
`, []string{"links: 2 configured"}},
		"relay": {minimal + "relay: {enabled: true, accept_from: [lte], forward_via: [lte]}\n", []string{"relay: relaying is not available"}},
		"unknown driver": {`
node_id: pi-1
links: [{id: lora, driver: lora-raw}]
`, []string{`unknown driver "lora-raw"`}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := node.ParseConfig([]byte(c.yaml))
			if err == nil {
				t.Fatal("accepted")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error does not mention %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestLoadConfigNamesAMissingFile(t *testing.T) {
	if _, err := node.LoadConfig(filepath.Join(t.TempDir(), "node.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err %v", err)
	}
}
