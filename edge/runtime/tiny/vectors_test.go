package tiny_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tiny"
)

const (
	vectorsFile    = "testdata/tiny-frame-v1.vectors.json"
	registrySHA256 = "562e38f5628f66232ee4394e278271995bfadaf5b7adfedd9894d1879d61332b"
	vectorsSHA256  = "3fd73becb0c9ea13f8fe0a47fbf2b084aa808d5c1a5ddfbe71303f339d2997d4"
)

type hexBytes []byte

func (h *hexBytes) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := hex.DecodeString(s)
	*h = v
	return err
}

type frameVector struct {
	Name         string          `json:"name"`
	Suite        tiny.Suite      `json:"suite"`
	Direction    string          `json:"direction"`
	Epoch        uint16          `json:"epoch"`
	NodeTag      uint32          `json:"node_tag"`
	Lane         int32           `json:"lane"`
	AckRequested bool            `json:"ack_requested"`
	More         bool            `json:"more"`
	Counter      uint32          `json:"counter"`
	Schema       uint16          `json:"schema"`
	Body         hexBytes        `json:"body"`
	Nonce        hexBytes        `json:"nonce"`
	Frame        hexBytes        `json:"frame"`
	FrameSize    int             `json:"frame_size"`
	Decoded      json.RawMessage `json:"decoded"`
}

type vectors struct {
	Inputs struct {
		RootKey      hexBytes `json:"root_key"`
		OtherRootKey hexBytes `json:"other_root_key"`
		Epoch        uint16   `json:"epoch"`
	} `json:"inputs"`
	KeySchedule []struct {
		Root    string `json:"root"`
		Epoch   uint16 `json:"epoch"`
		NodeTag uint32 `json:"node_tag"`
	} `json:"key_schedule"`
	Frames   []frameVector `json:"frames"`
	Negative []struct {
		Name   string   `json:"name"`
		Frame  hexBytes `json:"frame"`
		OpenAs string   `json:"open_as"`
		Error  string   `json:"error"`
	} `json:"negative"`
	ReplayWindow struct {
		Size  int `json:"size"`
		Steps []struct {
			Epoch   uint16 `json:"epoch"`
			Counter uint32 `json:"counter"`
			Accept  bool   `json:"accept"`
		} `json:"steps"`
	} `json:"replay_window"`
}

func load(t *testing.T) *vectors {
	t.Helper()
	raw, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != vectorsSHA256 {
		t.Fatalf("%s changed (sha256 %x); copy it from zqnt-api, never regenerate it here", vectorsFile, sum)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func direction(s string) tiny.Direction {
	if s == "downlink" {
		return tiny.Downlink
	}
	return tiny.Uplink
}

func TestVectorsMatchTheSpecCopy(t *testing.T) {
	spec := filepath.Join("..", "..", "..", "..", "..", "utils", "zqnt-api", "spec", "tiny-frame-v1.vectors.json")
	want, err := os.ReadFile(spec)
	if err != nil {
		t.Skipf("no zqnt-api checkout next to the SDK: %v", err)
	}
	got, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from %s", vectorsFile, spec)
	}
}

func TestRegistryIsTheZqntAPICopy(t *testing.T) {
	raw, err := os.ReadFile("tiny_schemas.json")
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != registrySHA256 {
		t.Fatalf("tiny_schemas.json changed (sha256 %x); copy it from zqnt-api proto/v3", sum)
	}
	spec := filepath.Join("..", "..", "..", "..", "..", "utils", "zqnt-api", "proto", "v3", "tiny_schemas.json")
	want, err := os.ReadFile(spec)
	if err != nil {
		t.Skipf("no zqnt-api checkout next to the SDK: %v", err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("tiny_schemas.json differs from %s", spec)
	}
}

func TestKeyScheduleVectors(t *testing.T) {
	v := load(t)
	roots := map[string][]byte{"root_key": v.Inputs.RootKey, "other_root_key": v.Inputs.OtherRootKey}
	for _, k := range v.KeySchedule {
		tag, err := tiny.NodeTag(roots[k.Root], k.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		if tag != k.NodeTag {
			t.Errorf("%s epoch %d: node tag %d, want %d", k.Root, k.Epoch, tag, k.NodeTag)
		}
	}
}

func TestSealReproducesEveryVector(t *testing.T) {
	v := load(t)
	for _, f := range v.Frames {
		k, err := tiny.DeriveEpoch(v.Inputs.RootKey, f.Suite, f.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		if k.NodeTag != f.NodeTag {
			t.Errorf("%s: node tag %d, want %d", f.Name, k.NodeTag, f.NodeTag)
		}
		got, err := k.Seal(direction(f.Direction), tiny.Message{Lane: edgelinkv3.Lane(f.Lane), AckRequested: f.AckRequested,
			More: f.More, Counter: f.Counter, Schema: f.Schema, Body: f.Body})
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if !bytes.Equal(got, f.Frame) {
			t.Errorf("%s: sealed %x, want %x", f.Name, got, f.Frame)
		}
		if len(got) != f.FrameSize || len(got) > tiny.MaxFrameSize {
			t.Errorf("%s: %d bytes", f.Name, len(got))
		}
	}
}

func TestOpenDecodesEveryVector(t *testing.T) {
	v := load(t)
	for _, f := range v.Frames {
		h, err := tiny.ParseHeader(f.Frame)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		k, err := tiny.DeriveEpoch(v.Inputs.RootKey, h.Suite, h.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		m, err := k.Open(direction(f.Direction), f.Frame)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if m.Schema != f.Schema || !bytes.Equal(m.Body, f.Body) || m.Counter != f.Counter ||
			int32(m.Lane) != f.Lane || m.AckRequested != f.AckRequested || m.More != f.More {
			t.Errorf("%s: opened %+v", f.Name, m)
		}
		checkDecoded(t, f, m)
	}
}

func checkDecoded(t *testing.T, f frameVector, m tiny.Message) {
	t.Helper()
	var want map[string]any
	if err := json.Unmarshal(f.Decoded, &want); err != nil {
		t.Fatal(err)
	}
	reg := tiny.Standard()
	var got map[string]any
	switch {
	case m.Schema == tiny.SchemaPosition:
		p, err := tiny.DecodePosition(m.Body)
		if err != nil {
			t.Fatal(err)
		}
		got = map[string]any{"time": seconds(t, p.Time), "latitude": p.Latitude, "longitude": p.Longitude,
			"altitude": p.Altitude, "speed": p.Speed, "heading": p.Heading, "battery": float64(p.Battery)}
	case m.Schema == tiny.SchemaAlarm:
		a, err := tiny.DecodeAlarm(m.Body)
		if err != nil {
			t.Fatal(err)
		}
		code, _ := reg.AlarmCode(a.Code)
		got = map[string]any{"time": seconds(t, a.Time), "code": float64(a.Code), "alert_code": code,
			"severity": float64(a.Severity), "latitude": nullable(a.Latitude), "longitude": nullable(a.Longitude)}
	case m.Schema == tiny.SchemaStatus:
		s, err := tiny.DecodeStatus(m.Body)
		if err != nil {
			t.Fatal(err)
		}
		got = map[string]any{"time": seconds(t, s.Time), "battery": nullableInt(s.Battery), "voltage": nullable(s.Voltage),
			"flags": float64(s.Flags), "temperature": pointer(s.Temperature), "rssi": pointer(s.RSSI)}
	case m.Schema == tiny.SchemaCommandReply:
		r, err := tiny.DecodeCommandReply(m.Body)
		if err != nil {
			t.Fatal(err)
		}
		code, _ := reg.ErrorCode(r.Error)
		got = map[string]any{"downlink_seq": float64(r.DownlinkSeq), "state": float64(r.State), "error": float64(r.Error),
			"error_code": code, "result": hex.EncodeToString(r.Result)}
	case m.Schema == tiny.SchemaCommandCancel:
		c, err := tiny.DecodeCommandCancel(m.Body)
		if err != nil {
			t.Fatal(err)
		}
		got = map[string]any{"downlink_seq": float64(c.DownlinkSeq), "expires": seconds(t, c.Expires)}
	case tiny.IsCommandSchema(m.Schema):
		c, err := tiny.DecodeCommand(m.Schema, m.Body)
		if err != nil {
			t.Fatal(err)
		}
		schema, ok := reg.CommandSchema(want["command_id"].(string))
		if !ok || schema != c.Schema {
			t.Errorf("%s: registry has schema %d for %v", f.Name, schema, want["command_id"])
		}
		decoded, err := reg.DecodeParams(c.Schema, c.Params)
		if err != nil {
			t.Fatal(err)
		}
		got = map[string]any{"command_id": want["command_id"], "downlink_seq": float64(c.DownlinkSeq),
			"expires": seconds(t, c.Expires), "params": hex.EncodeToString(c.Params), "params_decoded": decoded}
	default:
		t.Fatalf("%s: schema %d", f.Name, m.Schema)
	}
	for key, w := range want {
		if !equal(got[key], w) {
			t.Errorf("%s: %s = %v, want %v", f.Name, key, got[key], w)
		}
	}
}

func TestNegativeVectorsAreRefused(t *testing.T) {
	v := load(t)
	for _, n := range v.Negative {
		err := open(v.Inputs.RootKey, direction(n.OpenAs), n.Frame)
		var want error
		switch n.Error {
		case "malformed":
			want = tiny.ErrMalformed
		case "authentication":
			want = tiny.ErrAuthentication
		default:
			t.Fatalf("%s: unknown error %q", n.Name, n.Error)
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", n.Name, err, want)
		}
	}
}

func TestAnotherNodesKeyDoesNotOpenAFrame(t *testing.T) {
	v := load(t)
	f := v.Frames[0]
	k, err := tiny.DeriveEpoch(v.Inputs.OtherRootKey, f.Suite, f.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Open(tiny.Uplink, f.Frame); !errors.Is(err, tiny.ErrWrongKey) {
		t.Errorf("open with another node's key: %v", err)
	}
}

func TestReplayWindowVector(t *testing.T) {
	v := load(t)
	if v.ReplayWindow.Size != tiny.ReplayWindowSize {
		t.Fatalf("window size %d, want %d", tiny.ReplayWindowSize, v.ReplayWindow.Size)
	}
	var w tiny.ReplayWindow
	for i, s := range v.ReplayWindow.Steps {
		if got := w.Accept(s.Epoch, s.Counter); got != s.Accept {
			t.Errorf("step %d (epoch %d, counter %d): accept %v, want %v", i, s.Epoch, s.Counter, got, s.Accept)
		}
	}
}

func open(root []byte, dir tiny.Direction, frame []byte) error {
	h, err := tiny.ParseHeader(frame)
	if err != nil {
		return err
	}
	k, err := tiny.DeriveEpoch(root, h.Suite, h.Epoch)
	if err != nil {
		return err
	}
	_, err = k.Open(dir, frame)
	return err
}

func seconds(t *testing.T, at interface{ Unix() int64 }) float64 {
	t.Helper()
	return float64(at.Unix() - tiny.TimeBase.Unix())
}

func nullable(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	return v
}

func nullableInt(v int) any {
	if v < 0 {
		return nil
	}
	return float64(v)
}

func pointer(v *int8) any {
	if v == nil {
		return nil
	}
	return float64(*v)
}

func equal(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}
