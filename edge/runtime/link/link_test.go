package link

import (
	"context"
	"strings"
	"testing"
)

type stubDriver struct {
	cfg LinkConfig
}

func (s *stubDriver) ID() string                 { return s.cfg.ID }
func (s *stubDriver) Caps() Caps                 { return Caps{MaxPayload: 1} }
func (s *stubDriver) Open(context.Context) error { return nil }
func (s *stubDriver) Close() error               { return nil }
func (s *stubDriver) Send(context.Context, []byte, SendMeta) SendResult {
	return SendResult{Status: Down}
}
func (s *stubDriver) Receive() <-chan RxFrame   { return nil }
func (s *stubDriver) Metrics() Metrics          { return Metrics{} }
func (s *stubDriver) State() <-chan StateChange { return nil }
func (s *stubDriver) MeshInfo() MeshInfo        { return MeshInfo{Kind: MeshNone} }
func (s *stubDriver) Neighbors() []Neighbor     { return nil }

func init() {
	Register("stub", func(cfg LinkConfig) (Driver, error) { return &stubDriver{cfg: cfg}, nil })
}

func TestRegisterTwicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	Register("stub", func(LinkConfig) (Driver, error) { return nil, nil })
}

func TestNewUnknownDriver(t *testing.T) {
	_, err := New(LinkConfig{ID: "lte", Driver: "carrier-pigeon"})
	if err == nil || !strings.Contains(err.Error(), "carrier-pigeon") || !strings.Contains(err.Error(), "stub") {
		t.Fatalf("err %v", err)
	}
	if !Registered("stub") || Registered("carrier-pigeon") {
		t.Fatal("Registered is wrong")
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
node_id: edge-7
links:
  - id: lte
    driver: stub
    cost: METERED
  - id: lora
    driver: stub
    port: /dev/ttyUSB0
    hop_limit: 3
relay:
  enabled: true
  accept_from: [lora]
  forward_via: [lte]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Links) != 2 || cfg.Links[1].ID != "lora" || cfg.Links[1].Options["hop_limit"] != 3 {
		t.Fatalf("links %+v", cfg.Links)
	}
	if _, inOptions := cfg.Links[0].Options["id"]; inOptions {
		t.Fatal("id leaked into the driver options")
	}
	port, err := cfg.Links[1].String("port", "")
	if err != nil || port != "/dev/ttyUSB0" {
		t.Fatalf("port %q %v", port, err)
	}
	if _, err := cfg.Links[1].String("hop_limit", ""); err == nil {
		t.Fatal("a number read as a string")
	}
	if _, err := cfg.Links[0].RequiredString("port"); err == nil {
		t.Fatal("missing required option accepted")
	}
	drivers, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(drivers) != 2 || drivers[0].ID() != "lte" || drivers[1].ID() != "lora" {
		t.Fatalf("built %v", drivers)
	}
}

func TestParseConfigRejects(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"unknown driver": {"links: [{id: a, driver: nope}]", `unknown driver "nope"`},
		"missing driver": {"links: [{id: a}]", "driver is required"},
		"missing id":     {"links: [{driver: stub}]", "id is required"},
		"duplicate id":   {"links: [{id: a, driver: stub}, {id: a, driver: stub}]", `duplicate id "a"`},
		"relay link":     {"links: [{id: a, driver: stub}]\nrelay: {enabled: true, accept_from: [a], forward_via: [b]}", `unknown link "b"`},
		"relay empty":    {"links: [{id: a, driver: stub}]\nrelay: {enabled: true}", "accept_from and forward_via"},
		"not yaml":       {"links: [", "config"},
	} {
		_, err := ParseConfig([]byte(tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want %q", name, err, tc.want)
		}
	}
}

func TestStateFeedKeepsTheLatest(t *testing.T) {
	f := NewStateFeed(StateDown, "not open")
	f.Publish(StateUp, "")
	f.Publish(StateDegraded, "loss")
	f.Publish(StateDegraded, "loss")
	s := <-f.C()
	if s.State != StateDegraded || s.Reason != "loss" {
		t.Fatalf("got %+v", s)
	}
	select {
	case s := <-f.C():
		t.Fatalf("unexpected %+v", s)
	default:
	}
	f.Close()
	f.Close()
	f.Publish(StateUp, "")
	if _, ok := <-f.C(); ok {
		t.Fatal("channel open after Close")
	}
	if f.Current().State != StateDegraded {
		t.Fatalf("current %+v", f.Current())
	}
}

func TestEnumsMatchTheContract(t *testing.T) {
	for k, want := range map[MeshKind]int32{MeshNone: 1, MeshIP: 2, MeshFlood: 3, MeshRouted: 4, MeshSingleHop: 5} {
		if int32(k) != want {
			t.Errorf("%s = %d, zqnt.edgelink.v3 has %d", k, k, want)
		}
	}
	for s, want := range map[LinkState]int32{StateUp: 1, StateDegraded: 2, StateDown: 3} {
		if int32(s) != want {
			t.Errorf("%s = %d, zqnt.edgelink.v3 has %d", s, s, want)
		}
	}
	for in, want := range map[string]MeshKind{"FLOOD": MeshFlood, "single-hop": MeshSingleHop, "ip_mesh": MeshIP} {
		if got, err := ParseMeshKind(in); err != nil || got != want {
			t.Errorf("ParseMeshKind(%q) = %s, %v", in, got, err)
		}
	}
	if _, err := ParseMeshKind("STAR"); err == nil {
		t.Error("STAR parsed")
	}
	if Sent.String() != "SENT" || DutyCycleExceeded.String() != "DUTY_CYCLE_EXCEEDED" || SendStatus(0).String() != "SendStatus(0)" {
		t.Error("SendStatus strings")
	}
}
