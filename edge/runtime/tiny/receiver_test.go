package tiny_test

import (
	"errors"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/tiny"
)

func vector(t *testing.T, v *vectors, name string) frameVector {
	t.Helper()
	for _, f := range v.Frames {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no vector %q", name)
	return frameVector{}
}

func receiver(t *testing.T, v *vectors) *tiny.Receiver {
	t.Helper()
	r, err := tiny.NewReceiver(v.Inputs.RootKey, tiny.SuiteChaCha20Poly1305)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReceiverOpensTheCommandVector(t *testing.T) {
	v := load(t)
	f := vector(t, v, "command")
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	in, err := receiver(t, v).Receive(f.Frame, now)
	if err != nil {
		t.Fatal(err)
	}
	if in.Command == nil || in.Cancel != nil {
		t.Fatalf("got %+v", in)
	}
	if in.Command.Schema != 263 || in.Command.DownlinkSeq != 43 || in.Command.Expires != tiny.Time(58017720) {
		t.Errorf("command %+v", in.Command)
	}
	if in.Header.NodeTag != f.NodeTag || in.Header.Counter != f.Counter || in.Header.Epoch != f.Epoch {
		t.Errorf("header %+v", in.Header)
	}
	params, err := tiny.Standard().DecodeParams(in.Command.Schema, in.Command.Params)
	if err != nil {
		t.Fatal(err)
	}
	if params["latitude"] != 48.14 || params["longitude"] != 11.58 || params["altitude"] != 40 {
		t.Errorf("params %v", params)
	}
}

func TestReceiverOpensTheCancelVector(t *testing.T) {
	v := load(t)
	f := vector(t, v, "command_cancel")

	in, err := receiver(t, v).Receive(f.Frame, time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if in.Cancel == nil || in.Command != nil || in.Cancel.DownlinkSeq != 43 || in.Cancel.Expires != tiny.Time(58017780) {
		t.Errorf("got %+v", in)
	}
}

func TestReceiverDropsAReplayedFrame(t *testing.T) {
	v := load(t)
	f := vector(t, v, "command")
	r := receiver(t, v)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	if _, err := r.Receive(f.Frame, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Receive(f.Frame, now); !errors.Is(err, tiny.ErrReplay) {
		t.Errorf("second receive: %v", err)
	}
}

func TestReceiverReportsAnExpiredCommandWithItsSeq(t *testing.T) {
	v := load(t)
	f := vector(t, v, "command")

	in, err := receiver(t, v).Receive(f.Frame, tiny.Time(58017720))
	if !errors.Is(err, tiny.ErrExpired) {
		t.Fatalf("got %v", err)
	}
	if in.Command == nil || in.Command.DownlinkSeq != 43 {
		t.Errorf("expired command not named: %+v", in)
	}
}

func TestReceiverRunsEachDownlinkSeqOfAnEpochOnce(t *testing.T) {
	v := load(t)
	f := vector(t, v, "command")
	k, err := tiny.DeriveEpoch(v.Inputs.RootKey, tiny.SuiteChaCha20Poly1305, f.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	resent, err := k.Seal(tiny.Downlink, tiny.Message{Lane: edgelinkv3.Lane_LANE_COMMAND_REPLY, Counter: 9,
		Schema: f.Schema, Body: f.Body})
	if err != nil {
		t.Fatal(err)
	}
	r := receiver(t, v)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	if _, err := r.Receive(f.Frame, now); err != nil {
		t.Fatal(err)
	}
	in, err := r.Receive(resent, now)
	if !errors.Is(err, tiny.ErrDuplicateCommand) || in.Command == nil || in.Command.DownlinkSeq != 43 {
		t.Errorf("resent command: %v, %+v", err, in)
	}
}

func TestReceiverRefusesUplinkFramesAndOtherSuites(t *testing.T) {
	v := load(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	if _, err := receiver(t, v).Receive(vector(t, v, "position").Frame, now); !errors.Is(err, tiny.ErrAuthentication) {
		t.Errorf("uplink frame: %v", err)
	}
	aes, err := tiny.NewReceiver(v.Inputs.RootKey, tiny.SuiteAES256GCM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aes.Receive(vector(t, v, "command").Frame, now); !errors.Is(err, tiny.ErrWrongKey) {
		t.Errorf("other suite: %v", err)
	}
	other, err := tiny.NewReceiver(v.Inputs.OtherRootKey, tiny.SuiteChaCha20Poly1305)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Receive(vector(t, v, "command").Frame, now); !errors.Is(err, tiny.ErrWrongKey) {
		t.Errorf("other node: %v", err)
	}
	for _, n := range v.Negative {
		if _, err := receiver(t, v).Receive(n.Frame, now); err == nil {
			t.Errorf("%s accepted", n.Name)
		}
	}
}

func TestReceiverDropsUplinkSchemasInTheCommandLane(t *testing.T) {
	v := load(t)
	k, err := tiny.DeriveEpoch(v.Inputs.RootKey, tiny.SuiteChaCha20Poly1305, v.Inputs.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	body, err := tiny.Position{Time: tiny.TimeBase, Latitude: 1, Longitude: 2}.Body()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := k.Seal(tiny.Downlink, tiny.Message{Lane: edgelinkv3.Lane_LANE_COMMAND_REPLY, Counter: 1,
		Schema: tiny.SchemaPosition, Body: body})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := receiver(t, v).Receive(frame, tiny.TimeBase); !errors.Is(err, tiny.ErrMalformed) {
		t.Errorf("got %v", err)
	}
}

func TestZeroedReceiverOpensNothing(t *testing.T) {
	v := load(t)
	r := receiver(t, v)
	r.Zero()

	if _, err := r.Receive(vector(t, v, "command").Frame, time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Error("opened a frame after Zero")
	}
}

func TestDecodeParamsChecksTheLayout(t *testing.T) {
	reg := tiny.Standard()
	if _, err := reg.DecodeParams(263, []byte{1, 2, 3}); !errors.Is(err, tiny.ErrMalformed) {
		t.Errorf("short params: %v", err)
	}
	if _, err := reg.DecodeParams(0x8000, nil); !errors.Is(err, tiny.ErrMalformed) {
		t.Errorf("organization schema: %v", err)
	}
	none, err := reg.DecodeParams(258, nil)
	if err != nil || len(none) != 0 {
		t.Errorf("flight.land: %v %v", none, err)
	}
}
