package adaptergrpc

import (
	"context"
	"io"
	"log/slog"
	"math"
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
