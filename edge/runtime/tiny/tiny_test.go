package tiny_test

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tiny"
)

var root = bytes.Repeat([]byte{0x5a}, tiny.RootKeySize)

func TestRegistryMatchesTheCodec(t *testing.T) {
	reg := tiny.Standard()
	want := map[uint16]struct {
		name, direction, lane string
		sizes                 []int
	}{
		tiny.SchemaPosition:      {"POSITION", "uplink", "LANE_TELEMETRY", []int{17}},
		tiny.SchemaAlarm:         {"ALARM", "uplink", "LANE_ALARM", []int{7, 15}},
		tiny.SchemaStatus:        {"STATUS", "uplink", "LANE_STATUS", []int{9}},
		tiny.SchemaCommandReply:  {"COMMAND_REPLY", "uplink", "LANE_COMMAND_REPLY", nil},
		tiny.SchemaCommandCancel: {"COMMAND_CANCEL", "downlink", "LANE_COMMAND_REPLY", []int{6}},
	}
	if len(reg.Schemas) != len(want) {
		t.Fatalf("registry has %d schemas, codec %d", len(reg.Schemas), len(want))
	}
	for _, s := range reg.Schemas {
		w, ok := want[s.ID]
		if !ok || s.Name != w.name || s.Direction != w.direction || s.Lane != w.lane {
			t.Errorf("schema %d: %+v", s.ID, s)
		}
		if w.sizes != nil && !equalInts(s.BodySizes, w.sizes) {
			t.Errorf("schema %s: sizes %v, want %v", s.Name, s.BodySizes, w.sizes)
		}
		if _, ok := edgelinkv3.Lane_value[s.Lane]; !ok {
			t.Errorf("schema %s: lane %s", s.Name, s.Lane)
		}
	}
	reply := reg.Schemas[3]
	if reply.BodyMin != 5 || reply.BodyMax != tiny.MaxBodySize {
		t.Errorf("COMMAND_REPLY sizes %d..%d", reply.BodyMin, reply.BodyMax)
	}
	for _, c := range reg.Commands {
		if !tiny.IsCommandSchema(c.ID) {
			t.Errorf("command %s has schema %d outside the command range", c.CommandID, c.ID)
		}
	}
}

func TestStampCapabilitiesFillsOnlyStandardCommands(t *testing.T) {
	set := &capabilityv3.CapabilitySet{Capabilities: []*capabilityv3.Capability{
		{CommandId: "flight.return_to_home"},
		{CommandId: "camera.take_photo"},
		{CommandId: "navigation.go_to", TinySchemaId: 0x8001},
	}}
	tiny.Standard().StampCapabilities(set)
	got := []uint32{set.Capabilities[0].TinySchemaId, set.Capabilities[1].TinySchemaId, set.Capabilities[2].TinySchemaId}
	if got[0] != 257 || got[1] != 0 || got[2] != 0x8001 {
		t.Errorf("tiny schema ids %v", got)
	}
}

func TestFramesStayWithin48Bytes(t *testing.T) {
	k, err := tiny.DeriveEpoch(root, tiny.SuiteChaCha20Poly1305, 700)
	if err != nil {
		t.Fatal(err)
	}
	f, err := k.Seal(tiny.Uplink, tiny.Message{Lane: edgelinkv3.Lane_LANE_BULK, Counter: 1, Schema: 0x8000,
		Body: make([]byte, tiny.MaxBodySize)})
	if err != nil || len(f) != tiny.MaxFrameSize {
		t.Fatalf("largest frame: %d bytes, %v", len(f), err)
	}
	if _, err := k.Seal(tiny.Uplink, tiny.Message{Lane: edgelinkv3.Lane_LANE_BULK, Counter: 2, Schema: 0x8000,
		Body: make([]byte, tiny.MaxBodySize+1)}); !errors.Is(err, tiny.ErrMalformed) {
		t.Errorf("oversized body: %v", err)
	}
	for _, m := range []tiny.Message{
		{Lane: edgelinkv3.Lane_LANE_ALARM, Counter: 0, Schema: 1},
		{Lane: edgelinkv3.Lane_LANE_UNSPECIFIED, Counter: 1, Schema: 1},
		{Lane: edgelinkv3.Lane_LANE_ALARM, Counter: 1, Schema: 0},
	} {
		if _, err := k.Seal(tiny.Uplink, m); !errors.Is(err, tiny.ErrMalformed) {
			t.Errorf("seal %+v: %v", m, err)
		}
	}
}

func TestBodiesRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 3, 8, 30, 15, 0, time.UTC)
	p := tiny.Position{Time: at, Latitude: -33.8688197, Longitude: 151.2092955, Altitude: math.NaN(), Speed: math.NaN(),
		Heading: 359, Battery: -1}
	b, err := p.Body()
	if err != nil {
		t.Fatal(err)
	}
	got, err := tiny.DecodePosition(b)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Time.Equal(at) || got.Latitude != -33.8688197 || got.Longitude != 151.2092955 || !math.IsNaN(got.Altitude) ||
		!math.IsNaN(got.Speed) || got.Heading != 0 || got.Battery != -1 {
		t.Errorf("position %+v", got)
	}
	if _, err := (tiny.Position{Time: at, Latitude: math.NaN(), Longitude: 1}).Body(); err == nil {
		t.Error("a position without coordinates was encoded")
	}
	if _, err := (tiny.Position{Time: tiny.TimeBase.Add(-time.Second)}).Body(); err == nil {
		t.Error("a time before the time base was encoded")
	}
	reply := tiny.CommandReply{DownlinkSeq: 7, State: 4, Error: 3, Result: make([]byte, tiny.MaxResultSize+1)}
	if _, err := reply.Body(); !errors.Is(err, tiny.ErrMalformed) {
		t.Errorf("oversized result: %v", err)
	}
	cmd := tiny.Command{Schema: tiny.SchemaStatus, Expires: at}
	if _, err := cmd.Body(); !errors.Is(err, tiny.ErrMalformed) {
		t.Errorf("STATUS as a command: %v", err)
	}
}

func TestEpochAt(t *testing.T) {
	for _, c := range []struct {
		at   time.Time
		want uint16
	}{
		{tiny.TimeBase, 0},
		{tiny.TimeBase.Add(-time.Hour), 0},
		{time.Date(2026, 11, 3, 23, 59, 59, 0, time.UTC), 671},
		{time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), 672},
	} {
		if got := tiny.EpochAt(c.at); got != c.want {
			t.Errorf("EpochAt(%s) = %d, want %d", c.at, got, c.want)
		}
	}
}

func TestEpochKeysDifferPerSuiteEpochAndNode(t *testing.T) {
	msg := tiny.Message{Lane: edgelinkv3.Lane_LANE_STATUS, Counter: 9, Schema: tiny.SchemaStatus, Body: make([]byte, 9)}
	seen := map[string]bool{}
	for _, r := range [][]byte{root, bytes.Repeat([]byte{0xa5}, tiny.RootKeySize)} {
		for _, s := range []tiny.Suite{tiny.SuiteChaCha20Poly1305, tiny.SuiteAES256GCM} {
			for _, e := range []uint16{1, 2} {
				k, err := tiny.DeriveEpoch(r, s, e)
				if err != nil {
					t.Fatal(err)
				}
				f, err := k.Seal(tiny.Uplink, msg)
				if err != nil {
					t.Fatal(err)
				}
				if seen[string(f[tiny.HeaderSize:])] {
					t.Errorf("same ciphertext for suite %d epoch %d", s, e)
				}
				seen[string(f[tiny.HeaderSize:])] = true
			}
		}
	}
	if _, err := tiny.DeriveEpoch(root[:16], tiny.SuiteAES256GCM, 1); err == nil {
		t.Error("a 128-bit root key was accepted")
	}
	if _, err := tiny.DeriveEpoch(root, tiny.Suite(3), 1); err == nil {
		t.Error("suite 3 was accepted")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
