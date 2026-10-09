package adapter

import (
	"context"
	"math"
	"testing"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/schema"
)

var takeoffSchema = map[string]any{
	"type":     "object",
	"required": []any{"altitude"},
	"properties": map[string]any{
		"altitude": map[string]any{"type": "number"},
		"climbs":   map[string]any{"type": "integer"},
	},
}

type drone struct {
	Base
	got []*domains.CustomCommandRequest
}

func newDrone(t *testing.T) *drone {
	d := &drone{}
	if err := d.RegisterCommand("flight.takeoff", takeoffSchema, nil, func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		d.got = append(d.got, req)
		return domains.Success("airborne", req.SN), nil
	}, WithCompletion(domains.CompletionAsynchronous, "flight.takeoff.completed")); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCapabilitiesComeFromTheRegistrationsOnly(t *testing.T) {
	d := newDrone(t)
	d.SetAssetType("ASSET_TYPE_DRONE")
	d.DeclareTelemetryField(domains.TelemetryField{Key: "drone.gear", Type: domains.TelemetryValueNumber})

	caps, _ := d.GetCapabilities(context.Background(), "SN-1")
	if len(caps.Capabilities) != 1 || caps.Capabilities[0].Command != "flight.takeoff" {
		t.Fatalf("capabilities = %+v", caps.Capabilities)
	}
	c := caps.Capabilities[0]
	if !c.Available || c.Completion != domains.CompletionAsynchronous || c.InputSchema == nil ||
		c.TargetType != domains.CapabilityTargetAsset || c.Source != domains.CapabilitySourceEdgeAdapter {
		t.Errorf("capability = %+v", c)
	}
	if caps.AssetType != "ASSET_TYPE_DRONE" || len(caps.TelemetryFields) != 1 {
		t.Errorf("asset type %q, telemetry fields %+v", caps.AssetType, caps.TelemetryFields)
	}
}

func TestInvalidParamsAreRejectedBeforeTheHandlerRuns(t *testing.T) {
	d := newDrone(t)
	r, err := d.Execute(context.Background(), &domains.CustomCommandRequest{SN: "SN-1", CommandID: "flight.takeoff",
		Params: map[string]any{"altitude": "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsRejected() || r.ErrorCode != schema.InvalidParamsCode {
		t.Fatalf("result = %+v, want rejected with %s", r, schema.InvalidParamsCode)
	}
	if len(d.got) != 0 {
		t.Fatal("the handler ran on invalid params")
	}
}

func TestIntegersArriveAsInt64(t *testing.T) {
	d := newDrone(t)
	if _, err := d.Execute(context.Background(), &domains.CustomCommandRequest{CommandID: "flight.takeoff",
		Params: map[string]any{"altitude": 30.0, "climbs": 2.0}}); err != nil {
		t.Fatal(err)
	}
	if v, ok := d.got[0].Params["climbs"].(int64); !ok || v != 2 {
		t.Errorf("climbs = %#v, want int64(2)", d.got[0].Params["climbs"])
	}
}

func TestTypedV2MethodRunsTheRegisteredHandler(t *testing.T) {
	d := newDrone(t)
	r, _ := d.TakeOff(context.Background(), &domains.TakeOffRequest{SN: "SN-1", TID: "capexec:e:n",
		Coordinates: domains.Coordinates{Lat: 52.5, Lon: 13.4, Alt: 40}})
	if !r.IsSuccess() || len(d.got) != 1 {
		t.Fatalf("result %+v, calls %d", r, len(d.got))
	}
	if d.got[0].TID != "capexec:e:n" || d.got[0].Params["altitude"] != 40.0 {
		t.Errorf("request = %+v", d.got[0])
	}
}

func TestOmittedCoordinatesAreLeftOutNotZero(t *testing.T) {
	d := &drone{}
	d.MustRegisterCommand("navigation.go_to", nil, nil, func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		d.got = append(d.got, req)
		return domains.Success("", req.SN), nil
	})
	_, _ = d.GoTo(context.Background(), &domains.GoToRequest{Coordinates: domains.Coordinates{Lat: 1, Lon: 2, Alt: math.NaN()}})
	if _, ok := d.got[0].Params["altitude"]; ok {
		t.Errorf("NaN altitude was sent as %v", d.got[0].Params["altitude"])
	}
}

func TestUnregisteredCommandIsNotImplemented(t *testing.T) {
	d := newDrone(t)
	r, _ := d.OpenCover(context.Background(), "SN-1")
	if !r.IsNotImplemented() {
		t.Fatalf("result = %+v", r)
	}
}

func TestRegistrationChangesAreAnnounced(t *testing.T) {
	d := newDrone(t)
	calls := 0
	d.OnChange(func() { calls++ })
	d.MustRegisterCommand("dock.open_cover", nil, nil, func(context.Context, *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		return nil, nil
	})
	d.UnregisterCommand("dock.open_cover")
	d.UnregisterCommand("dock.open_cover")
	if calls != 2 {
		t.Fatalf("listener called %d times, want 2", calls)
	}
}

func TestBrokenSchemaIsRefusedAtRegistration(t *testing.T) {
	d := &drone{}
	err := d.RegisterCommand("x.y", map[string]any{"type": 1}, nil, func(context.Context, *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		return nil, nil
	})
	if err == nil || d.Has("x.y") {
		t.Fatalf("err = %v, registered = %v", err, d.Has("x.y"))
	}
}
