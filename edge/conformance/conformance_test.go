package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
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

// legacyDrone breaks every rule once.
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

func (legacyDrone) TakeOff(_ context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
	return domains.Success("up", req.SN), nil
}

func (legacyDrone) SendCustomCommand(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	if strings.HasPrefix(req.CommandID, "vendor.acme.") {
		return domains.Success("ok", req.SN), nil
	}
	return domains.NotImplemented("no", req.SN), nil
}

func TestEveryRuleIsChecked(t *testing.T) {
	r := Check(legacyDrone{}, Options{Events: &Recorder{}, CompletionTimeout: 50 * time.Millisecond})
	expect := map[string][]string{
		"schemas":    r.Schemas,
		"advertised": r.Advertised,
		"executable": r.Executable,
		"completion": r.Completion,
	}
	want := map[string]string{
		"schemas":    "vendor.acme.spray: input schema does not parse",
		"advertised": "dock.open_cover is advertised but not executable",
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
