package adaptergrpc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// v3 has no per-command RPCs. An adapter written for v2 -- typed TakeOff, its own
// SendCustomCommand for vendor ids -- must answer v3 ExecuteCommand unchanged, and an id it
// cannot place is REJECTED, never an error status.
type v3Adapter struct {
	adapter.UnimplementedEdgeAdapter
	takeoffs []*domains.TakeOffRequest
	custom   []*domains.CustomCommandRequest
	stopped  []string
}

func (a *v3Adapter) TakeOff(_ context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
	a.takeoffs = append(a.takeoffs, req)
	return domains.Success("airborne", req.SN), nil
}

func (a *v3Adapter) StopTask(_ context.Context, sn, taskID, _ string) (*domains.CommandResult, error) {
	a.stopped = append(a.stopped, taskID)
	return domains.Success("stopped", sn), nil
}

func (a *v3Adapter) SendCustomCommand(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	a.custom = append(a.custom, req)
	switch req.CommandID {
	case "vendor.acme.spray":
		return domains.Success("sprayed", req.SN), nil
	case "vendor.acme.broken":
		return domains.Error("nozzle blocked", req.SN), nil
	}
	return domains.NotImplemented("unknown "+req.CommandID, req.SN), nil
}

func execute(t *testing.T, a *v3Adapter, id string, params map[string]any) *capabilityv3.CommandResult {
	t.Helper()
	p, err := structpb.NewStruct(params)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServerV3(a, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := s.ExecuteCommand(context.Background(), &edgev3.ExecuteCommandRequest{
		CommandExecutionId: "cx-1",
		Command: &capabilityv3.Command{
			Asset:     &commonv3.AssetRef{Sn: "SN-1"},
			CommandId: id,
			Params:    p,
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand returned an error status: %v", err)
	}
	return resp.GetResult()
}

func TestV3BuiltInIDRunsTheTypedMethod(t *testing.T) {
	a := &v3Adapter{}
	r := execute(t, a, "flight.takeoff", map[string]any{"latitude": 52.5, "longitude": 13.4})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("state = %v", r.GetState())
	}
	if len(a.takeoffs) != 1 || a.takeoffs[0].Coordinates.Lat != 52.5 {
		t.Fatalf("takeoff not routed: %+v", a.takeoffs)
	}
	if !math.IsNaN(a.takeoffs[0].Coordinates.Alt) {
		t.Errorf("omitted altitude must be NaN, got %v", a.takeoffs[0].Coordinates.Alt)
	}
	if r.GetCommandExecutionId() != "cx-1" {
		t.Errorf("command execution id = %q", r.GetCommandExecutionId())
	}
}

func TestV3VendorIDGoesToSendCustomCommand(t *testing.T) {
	a := &v3Adapter{}
	r := execute(t, a, "vendor.acme.spray", map[string]any{"seconds": 3})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED || len(a.custom) != 1 {
		t.Fatalf("state = %v, custom = %d", r.GetState(), len(a.custom))
	}
	if a.custom[0].Params["seconds"] != 3.0 {
		t.Errorf("params = %v", a.custom[0].Params)
	}
}

func TestV3UnknownIDIsRejectedNotAnError(t *testing.T) {
	r := execute(t, &v3Adapter{}, "vendor.nope.nothing", nil)
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_REJECTED {
		t.Fatalf("state = %v", r.GetState())
	}
	if r.GetError().GetCode() != NotSupportedCode || r.GetError().GetCategory() != commonv3.ErrorCategory_ERROR_CATEGORY_INVALID_ARGUMENT {
		t.Errorf("error = %+v", r.GetError())
	}
}

func TestV3AdapterFailureIsFailed(t *testing.T) {
	r := execute(t, &v3Adapter{}, "vendor.acme.broken", nil)
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_FAILED || r.GetError().GetMessage() != "nozzle blocked" {
		t.Fatalf("result = %+v", r)
	}
}

func TestV3CancelStopsTheExecution(t *testing.T) {
	a := &v3Adapter{}
	s := NewServerV3(a, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := s.CancelCommand(context.Background(), &edgev3.CancelCommandRequest{CommandExecutionId: "dji-77"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetResult().GetState() != capabilityv3.CommandState_COMMAND_STATE_CANCELLED || len(a.stopped) != 1 || a.stopped[0] != "dji-77" {
		t.Fatalf("result = %+v, stopped = %v", resp.GetResult(), a.stopped)
	}
}

// declaringAdapter declares take-off asynchronous; its typed TakeOff still answers a plain success.
type declaringAdapter struct {
	v3Adapter
}

func (a *declaringAdapter) GetCapabilities(_ context.Context, sn string) (*domains.CurrentCapabilities, error) {
	return &domains.CurrentCapabilities{SN: sn, Capabilities: []domains.Capability{
		{Command: "flight.takeoff", Available: true, Completion: domains.CompletionAsynchronous,
			CompletionEvent: "flight.takeoff.completed",
			Events:          []domains.CapabilityEvent{{Name: "flight.takeoff.completed", Description: "airborne"}}},
		{Command: "vendor.acme.spray", Available: true, Completion: domains.CompletionOnReply},
	}}, nil
}

func executeOn(t *testing.T, a adapter.EdgeAdapter, id string, params map[string]any) *capabilityv3.CommandResult {
	t.Helper()
	p, err := structpb.NewStruct(params)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServerV3(a, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := s.ExecuteCommand(context.Background(), &edgev3.ExecuteCommandRequest{
		CommandExecutionId: "cx-1",
		Command:            &capabilityv3.Command{Asset: &commonv3.AssetRef{Sn: "SN-1"}, CommandId: id, Params: p},
	})
	if err != nil {
		t.Fatalf("ExecuteCommand returned an error status: %v", err)
	}
	return resp.GetResult()
}

func TestV3DeclaredAsynchronousCommandWaitsEvenOnAPlainSuccess(t *testing.T) {
	r := executeOn(t, &declaringAdapter{}, "flight.takeoff", map[string]any{"latitude": 52.5, "longitude": 13.4, "altitude": 40})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_ACCEPTED {
		t.Fatalf("state = %v, want ACCEPTED", r.GetState())
	}
	if r.GetCommandExecutionId() != "cx-1" {
		t.Fatalf("command_execution_id = %q", r.GetCommandExecutionId())
	}
}

func TestV3DeclaredOnReplyCommandIsDoneOnItsReply(t *testing.T) {
	r := executeOn(t, &declaringAdapter{}, "vendor.acme.spray", map[string]any{})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("state = %v, want SUCCEEDED", r.GetState())
	}
}

// acceptingAdapter answers with its own execution id, the way a mission is started.
type acceptingAdapter struct {
	v3Adapter
}

func (a *acceptingAdapter) SendCustomCommand(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	return domains.Accepted("flying", "sim-mission-7", req.SN), nil
}

func TestV3AnAdapterAcceptedResultCarriesItsOwnExecutionID(t *testing.T) {
	r := executeOn(t, &acceptingAdapter{}, "mission.waypoint.execute", map[string]any{})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_ACCEPTED {
		t.Fatalf("state = %v, want ACCEPTED", r.GetState())
	}
	if got := r.GetResult().GetFields()["external_execution_id"].GetStringValue(); got != "sim-mission-7" {
		t.Fatalf("external_execution_id = %q", got)
	}
}

func TestV3CompletionModeIsPublishedWithTheCapability(t *testing.T) {
	s := NewServerV3(&declaringAdapter{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := s.GetCapabilities(context.Background(), &edgev3.GetCapabilitiesRequest{Asset: &commonv3.AssetRef{Sn: "SN-1"}})
	if err != nil {
		t.Fatal(err)
	}
	takeoff := resp.GetCapabilities().GetCapabilities()[0]
	if takeoff.GetCompletion() != capabilityv3.CompletionMode_COMPLETION_MODE_ASYNCHRONOUS ||
		takeoff.GetCompletionEvent() != "flight.takeoff.completed" || takeoff.GetEvents()[0].GetName() != "flight.takeoff.completed" {
		t.Fatalf("capability = %v", takeoff)
	}
}

// registryDrone writes each command once; v3 and v2 both reach the same handler.
type registryDrone struct {
	adapter.Base
	got []*domains.CustomCommandRequest
}

func newRegistryDrone() *registryDrone {
	d := &registryDrone{}
	d.MustRegisterCommand("flight.takeoff", map[string]any{
		"type":       "object",
		"required":   []any{"altitude"},
		"properties": map[string]any{"altitude": map[string]any{"type": "number"}, "retries": map[string]any{"type": "integer"}},
	}, map[string]any{"type": "object"}, func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		d.got = append(d.got, req)
		return domains.SuccessWithOutput("airborne", req.SN, map[string]any{"altitude": req.Params["altitude"]}), nil
	})
	d.DeclareTelemetryField(domains.TelemetryField{Key: "drone.gear", Type: domains.TelemetryValueNumber, Unit: ""})
	return d
}

func TestV3RegisteredCommandRunsItsHandlerWithTheExecutionID(t *testing.T) {
	d := newRegistryDrone()
	r := executeOn(t, d, "flight.takeoff", map[string]any{"altitude": 40, "retries": 2})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("result = %v", r)
	}
	if r.GetResult().GetFields()["altitude"].GetNumberValue() != 40 {
		t.Errorf("result struct = %v", r.GetResult())
	}
	if d.got[0].CommandExecutionID != "cx-1" || d.got[0].TID != "cx-1" {
		t.Errorf("request = %+v", d.got[0])
	}
	if v, ok := d.got[0].Params["retries"].(int64); !ok || v != 2 {
		t.Errorf("retries = %#v, want int64", d.got[0].Params["retries"])
	}
}

func TestV3InvalidParamsAreRejectedWithAReadableMessage(t *testing.T) {
	d := newRegistryDrone()
	r := executeOn(t, d, "flight.takeoff", map[string]any{"altitude": "high"})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_REJECTED || r.GetError().GetCode() != "command.invalid_params" {
		t.Fatalf("result = %v", r)
	}
	if !strings.Contains(r.GetError().GetMessage(), "altitude") {
		t.Errorf("message %q does not name the field", r.GetError().GetMessage())
	}
	if len(d.got) != 0 {
		t.Fatal("handler ran on invalid params")
	}
}

// schemaAdapter is a v2-style adapter that only advertises a schema; the SDK still validates.
type schemaAdapter struct {
	v3Adapter
}

func (a *schemaAdapter) GetCapabilities(_ context.Context, sn string) (*domains.CurrentCapabilities, error) {
	return &domains.CurrentCapabilities{SN: sn, Capabilities: []domains.Capability{{
		Command: "camera.change_zoom", Available: true,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"zoom": map[string]any{"type": "integer", "minimum": 1}}},
	}}}, nil
}

func (a *schemaAdapter) ChangeZoom(_ context.Context, req *domains.ChangeZoomRequest) (*domains.CommandResult, error) {
	return domains.Success(fmt.Sprint(*req.Zoom), req.SN), nil
}

func TestV3AdvertisedSchemaIsEnforcedForTypedAdapters(t *testing.T) {
	if r := executeOn(t, &schemaAdapter{}, "camera.change_zoom", map[string]any{"zoom": 0}); r.GetState() != capabilityv3.CommandState_COMMAND_STATE_REJECTED {
		t.Fatalf("zoom 0 = %v, want REJECTED", r.GetState())
	}
	if r := executeOn(t, &schemaAdapter{}, "camera.change_zoom", map[string]any{"zoom": 4}); r.GetState() != capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("zoom 4 = %v, want SUCCEEDED", r)
	}
}

func TestV2TypedRPCOnARegistryAdapterReachesTheSameHandler(t *testing.T) {
	d := newRegistryDrone()
	r, _ := d.TakeOff(context.Background(), &domains.TakeOffRequest{SN: "SN-1", Coordinates: domains.Coordinates{Lat: math.NaN(), Lon: math.NaN(), Alt: 25}})
	if !r.IsSuccess() || d.got[0].Params["altitude"] != 25.0 {
		t.Fatalf("result %+v, request %+v", r, d.got)
	}
}

func TestV3TelemetryFieldsArePublishedWithTheCapabilities(t *testing.T) {
	s := NewServerV3(newRegistryDrone(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	resp, err := s.GetCapabilities(context.Background(), &edgev3.GetCapabilitiesRequest{Asset: &commonv3.AssetRef{Sn: "SN-1"}})
	if err != nil {
		t.Fatal(err)
	}
	fields := resp.GetCapabilities().GetTelemetryFields()
	if len(fields) != 1 || fields[0].GetKey() != "drone.gear" || fields[0].GetType() != capabilityv3.TelemetryValueType_TELEMETRY_VALUE_TYPE_NUMBER {
		t.Fatalf("telemetry fields = %v", fields)
	}
}
