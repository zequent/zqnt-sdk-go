package adaptergrpc

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	commonpb "github.com/zequent/zqnt-api/go/v3/gen/common/proto"
	devicecontrolpb "github.com/zequent/zqnt-api/go/v3/gen/devicecontrol/contracts/proto"
	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter/domains"
	"google.golang.org/protobuf/types/known/structpb"
)

const executionID = "capexec:exec-1:node-1"

var sprayInput = map[string]any{
	"type":     "object",
	"required": []any{"seconds"},
	"properties": map[string]any{
		"seconds":  map[string]any{"type": "integer"},
		"pressure": map[string]any{"type": "number"},
		"dryRun":   map[string]any{"type": "boolean"},
		"nozzle":   map[string]any{"type": "string", "enum": []any{"FINE", "WIDE"}},
		"label":    map[string]any{"type": "string"},
	},
}

// sprayer registers its commands and takes 2.x free-form commands through LegacyCustomCommand.
type sprayer struct {
	adapter.Base
	runs   []*domains.CustomCommandRequest
	legacy []string
}

func newSprayer() *sprayer {
	a := &sprayer{}
	record := func(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		a.runs = append(a.runs, req)
		return domains.Success("done", req.SN), nil
	}
	a.MustRegisterCommand("vendor.acme.spray", sprayInput, nil, record)
	a.MustRegisterCommand("flight.takeoff", nil, nil, record)
	return a
}

func (a *sprayer) LegacyCustomCommand(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	a.legacy = append(a.legacy, req.CommandID)
	return domains.Success("sent", req.SN), nil
}

func v2Server(a adapter.EdgeAdapter) *Server {
	return NewServer(a, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func v2Base() *commonpb.RequestBase {
	id := executionID
	return &commonpb.RequestBase{Tid: "tid-1", Sn: "SN-1", ExternalId: &id}
}

func sendCustom(t *testing.T, a adapter.EdgeAdapter, id string, params map[string]any) *devicecontrolpb.CustomCommandResponse {
	t.Helper()
	p, err := structpb.NewStruct(params)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := v2Server(a).SendCustomCommand(context.Background(), &devicecontrolpb.CustomCommandRequest{
		Base: v2Base(), CommandId: id, Params: p})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestV3RefusesAnUnregisteredIDEvenWhenTheAdapterTakesFreeFormCommands(t *testing.T) {
	a := newSprayer()
	r := executeOn(t, a, "Start Test", nil)
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_REJECTED || r.GetError().GetCode() != NotSupportedCode {
		t.Fatalf("result = %v", r)
	}
	if len(a.legacy) != 0 {
		t.Fatalf("LegacyCustomCommand ran for %v", a.legacy)
	}
}

func TestV2HandsAnUnregisteredIDToLegacyCustomCommand(t *testing.T) {
	a := newSprayer()
	resp := sendCustom(t, a, "Start Test", nil)
	if resp.GetHasErrors() || len(a.legacy) != 1 || a.legacy[0] != "Start Test" {
		t.Fatalf("response = %v, legacy = %v", resp, a.legacy)
	}
}

func TestLegacyCustomCommandRefusesByDefault(t *testing.T) {
	b := &adapter.Base{}
	resp := sendCustom(t, b, "Start Test", nil)
	if !resp.GetHasErrors() || !strings.Contains(resp.GetError().GetErrorMessage(), "not supported") {
		t.Fatalf("response = %v", resp)
	}
}

func TestV2RegisteredIDNeverReachesLegacyCustomCommand(t *testing.T) {
	a := newSprayer()
	if resp := sendCustom(t, a, "vendor.acme.spray", map[string]any{"seconds": 3}); resp.GetHasErrors() {
		t.Fatalf("response = %v", resp)
	}
	if len(a.legacy) != 0 || len(a.runs) != 1 {
		t.Fatalf("legacy = %v, runs = %d", a.legacy, len(a.runs))
	}
}

func TestV2CustomCommandHandsTheExecutionIDToTheHandler(t *testing.T) {
	a := newSprayer()
	sendCustom(t, a, "vendor.acme.spray", map[string]any{"seconds": 3})
	if a.runs[0].CommandExecutionID != executionID {
		t.Fatalf("command execution id = %q", a.runs[0].CommandExecutionID)
	}
}

func TestV2TypedCallHandsTheExecutionIDToTheHandler(t *testing.T) {
	a := newSprayer()
	resp, err := v2Server(a).TakeOff(context.Background(), &devicecontrolpb.CoordinateCommandRequest{
		Base: v2Base(), Coordinate: &commonpb.GeoCoordinate{Latitude: 52.5, Longitude: 13.4, Altitude: 30}})
	if err != nil || resp.GetHasErrors() {
		t.Fatalf("response = %v, err = %v", resp, err)
	}
	if a.runs[0].CommandExecutionID != executionID {
		t.Fatalf("command execution id = %q", a.runs[0].CommandExecutionID)
	}
}

func TestV2ConvertsTextToThePublishedTypes(t *testing.T) {
	a := newSprayer()
	resp := sendCustom(t, a, "vendor.acme.spray", map[string]any{
		"seconds": "5", "pressure": " 52.5200 ", "dryRun": "TRUE", "nozzle": "wide", "label": "0123"})
	if resp.GetHasErrors() {
		t.Fatalf("response = %v", resp)
	}
	p := a.runs[0].Params
	if p["seconds"] != int64(5) || p["pressure"] != 52.52 || p["dryRun"] != true || p["nozzle"] != "WIDE" || p["label"] != "0123" {
		t.Fatalf("params = %#v", p)
	}
}

func TestV2StillRefusesTextThatIsNotExactlyThePublishedType(t *testing.T) {
	a := newSprayer()
	resp := sendCustom(t, a, "vendor.acme.spray", map[string]any{"seconds": "3.5"})
	if !resp.GetHasErrors() || len(a.runs) != 0 {
		t.Fatalf("response = %v, runs = %d", resp, len(a.runs))
	}
}

func TestV3KeepsTextAsText(t *testing.T) {
	a := newSprayer()
	r := executeOn(t, a, "vendor.acme.spray", map[string]any{"seconds": "5"})
	if r.GetState() != capabilityv3.CommandState_COMMAND_STATE_REJECTED || r.GetError().GetCode() != "command.invalid_params" {
		t.Fatalf("result = %v", r)
	}
}
