package conformance

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter/domains"
)

type goodDrone struct {
	adapter.Base
	events adapter.CommandEventPublisher
}

func newGoodDrone(events adapter.CommandEventPublisher) *goodDrone {
	d := &goodDrone{events: events}
	d.MustRegisterCommand("flight.takeoff", map[string]any{
		"type":       "object",
		"required":   []any{"altitude"},
		"properties": map[string]any{"altitude": map[string]any{"type": "number", "minimum": 2}},
	}, nil, d.takeoff, adapter.WithCompletion(domains.CompletionAsynchronous, "flight.takeoff.completed"))
	d.MustRegisterCommand("dock.open_cover", nil, nil, func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		return domains.Success("open", req.SN), nil
	})
	return d
}

func (d *goodDrone) takeoff(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = d.events.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
			SN: req.SN, CommandExecutionID: req.CommandExecutionID, Status: domains.CommandExecutionSucceeded})
	}()
	return domains.Success("climbing", req.SN), nil
}

func TestAConformingAdapterPasses(t *testing.T) {
	events := &Recorder{}
	Run(t, newGoodDrone(events), Options{Events: events})
}

// legacyDrone advertises what only its free-form SendCustomCommand runs, and a broken schema.
type legacyDrone struct {
	adapter.UnimplementedEdgeAdapter
}

func (legacyDrone) GetCapabilities(_ context.Context, sn string) (*domains.CurrentCapabilities, error) {
	return &domains.CurrentCapabilities{SN: sn, Capabilities: []domains.Capability{
		{Command: "dock.open_cover", Available: true},
		{Command: "vendor.acme.spray", Available: true, InputSchema: map[string]any{"type": 7}},
		{Command: "vendor.acme.mission", Available: true, Completion: domains.CompletionAsynchronous},
	}}, nil
}

func (legacyDrone) SendCustomCommand(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	return domains.Success("ok", req.SN), nil
}

// sloppyDrone hides a registered command and never completes the one it accepts.
type sloppyDrone struct {
	adapter.Base
}

func newSloppyDrone() *sloppyDrone {
	d := &sloppyDrone{}
	ok := func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		return domains.Success("ok", req.SN), nil
	}
	d.MustRegisterCommand("flight.takeoff", nil, nil, ok)
	d.MustRegisterCommand("vendor.acme.mission", nil, nil, ok, adapter.WithCompletion(domains.CompletionAsynchronous, ""))
	return d
}

func (d *sloppyDrone) GetCapabilities(_ context.Context, sn string) (*domains.CurrentCapabilities, error) {
	caps := d.Capabilities(sn)
	caps.Capabilities = slices.DeleteFunc(caps.Capabilities, func(c domains.Capability) bool { return c.Command == "flight.takeoff" })
	return caps, nil
}

func TestEveryRuleIsChecked(t *testing.T) {
	opts := Options{Events: &Recorder{}, CompletionTimeout: 50 * time.Millisecond}
	legacy, sloppy := Check(legacyDrone{}, opts), Check(newSloppyDrone(), opts)
	expect := map[string][]string{
		"schemas":    legacy.Schemas,
		"advertised": legacy.Advertised,
		"free-form":  legacy.Advertised,
		"executable": sloppy.Executable,
		"completion": sloppy.Completion,
	}
	want := map[string]string{
		"schemas":    "vendor.acme.spray: input schema does not parse",
		"advertised": "dock.open_cover is advertised but not executable",
		"free-form":  "vendor.acme.mission is advertised but not executable",
		"executable": "flight.takeoff is executable",
		"completion": "vendor.acme.mission was ACCEPTED",
	}
	for rule, text := range want {
		found := false
		for _, p := range expect[rule] {
			found = found || strings.Contains(p, text)
		}
		if !found {
			t.Errorf("%s: want a problem containing %q, got %v", rule, text, expect[rule])
		}
	}
}

func TestAcceptedCommandsNeedARecorder(t *testing.T) {
	r := Check(newGoodDrone(&Recorder{}), Options{})
	if len(r.Completion) != 1 || !strings.Contains(r.Completion[0], "Options.Events") {
		t.Fatalf("completion problems = %v", r.Completion)
	}
}

func TestACommandTargetingAnUnreportedPayloadIsFound(t *testing.T) {
	events := &Recorder{}
	d := newGoodDrone(events)
	d.MustRegisterCommand("vendor.acme.announce", nil, nil, func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		return domains.Success("said", req.SN), nil
	}, adapter.WithTarget(domains.CapabilityTargetPayload, "speaker-1"))

	r := Check(d, Options{Events: events})
	if len(r.Payloads) != 1 || r.Payloads[0] != "vendor.acme.announce targets payload 'speaker-1', which is not reported" {
		t.Fatalf("payload problems %v", r.Payloads)
	}
	if err := d.SetPayloads("CONFORMANCE-1", []domains.Payload{{Ref: "speaker-1", Kind: "speaker"}}); err != nil {
		t.Fatal(err)
	}
	Run(t, d, Options{Events: events})
}
