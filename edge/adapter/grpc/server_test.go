package adaptergrpc

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	devicecontrolpb "github.com/zequent/zqnt-utils-golang/v2/gen/devicecontrol/contracts/proto"
)

// TestCapabilityTargetTypeToProto guards the exact bug that made GetCapabilities' Target
// unusable for mission-autonomy's own capability lookup (sameTarget requires an exact
// CapabilityTargetType match against the caller's configured target -- previously this SDK never
// set Target at all, leaving it at the proto zero value regardless of what the adapter declared).
func TestCapabilityTargetTypeToProto(t *testing.T) {
	cases := map[domains.CapabilityTargetType]devicecontrolpb.CapabilityTargetType{
		domains.CapabilityTargetUnspecified: devicecontrolpb.CapabilityTargetType_CAPABILITY_TARGET_TYPE_UNSPECIFIED,
		domains.CapabilityTargetAsset:       devicecontrolpb.CapabilityTargetType_CAPABILITY_TARGET_TYPE_ASSET,
		domains.CapabilityTargetSubAsset:    devicecontrolpb.CapabilityTargetType_CAPABILITY_TARGET_TYPE_SUB_ASSET,
		domains.CapabilityTargetPayload:     devicecontrolpb.CapabilityTargetType_CAPABILITY_TARGET_TYPE_PAYLOAD,
		domains.CapabilityTargetComponent:   devicecontrolpb.CapabilityTargetType_CAPABILITY_TARGET_TYPE_COMPONENT,
	}
	for in, want := range cases {
		if got := capabilityTargetTypeToProto(in); got != want {
			t.Errorf("capabilityTargetTypeToProto(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestCapabilitySourceToProto(t *testing.T) {
	cases := map[domains.CapabilitySource]devicecontrolpb.CapabilitySourceProto{
		domains.CapabilitySourceUnspecified: devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_UNSPECIFIED,
		domains.CapabilitySourceBuiltIn:     devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_BUILT_IN,
		domains.CapabilitySourceEdgeAdapter: devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_EDGE_ADAPTER,
		domains.CapabilitySourceRuntime:     devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_RUNTIME,
		domains.CapabilitySourceUser:        devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_USER,
		domains.CapabilitySourceApplication: devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_APPLICATION,
		domains.CapabilitySourceIntegration: devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_INTEGRATION,
		domains.CapabilitySourceAIGenerated: devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_AI_GENERATED,
	}
	for in, want := range cases {
		if got := capabilitySourceToProto(in); got != want {
			t.Errorf("capabilitySourceToProto(%v) = %v, want %v", in, got, want)
		}
	}
}

func testServer() *Server {
	return &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// The 2.0.0 command contract is what admin-console's SkillCatalogService harvests into the Skill
// Registry -- a field dropped here is a command the console can't render a form for.
func TestApplyCommandContract(t *testing.T) {
	in := &domains.Capability{
		Command:      "mission.waypoint.execute",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "null"},
		Errors:       []domains.CapabilityError{{Code: "NO_FIX", Description: "no GPS fix"}, {Code: "BUSY"}},
		Events:       []domains.CapabilityEvent{{Name: "leg.reached", PayloadSchema: map[string]any{"type": "object"}}},
		Requirements: &domains.CapabilityRequirements{
			AssetTypes: []string{"ASSET_TYPE_AIRCRAFT"},
			Properties: map[string]any{"battery.min": float64(20)},
		},
		SkillID:  "mission",
		Source:   domains.CapabilitySourceEdgeAdapter,
		Provider: "Zequent Simulator",
	}
	out := &devicecontrolpb.Capability{}
	testServer().applyCommandContract(in, out)

	if out.GetInputSchema().GetFields()["type"].GetStringValue() != "object" {
		t.Errorf("input schema not carried: %v", out.GetInputSchema())
	}
	if out.GetOutputSchema() == nil {
		t.Error("output schema not carried")
	}
	if len(out.GetErrors()) != 2 || out.GetErrors()[0].GetCode() != "NO_FIX" ||
		out.GetErrors()[0].GetDescription() != "no GPS fix" {
		t.Errorf("errors not carried: %v", out.GetErrors())
	}
	// An error with no description leaves the optional field unset rather than sending "".
	if out.GetErrors()[1].Description != nil {
		t.Errorf("empty description should stay unset, got %q", out.GetErrors()[1].GetDescription())
	}
	if len(out.GetEvents()) != 1 || out.GetEvents()[0].GetName() != "leg.reached" ||
		out.GetEvents()[0].GetPayloadSchema() == nil {
		t.Errorf("events not carried: %v", out.GetEvents())
	}
	if r := out.GetRequirements(); r == nil || len(r.GetAssetTypes()) != 1 ||
		r.GetProperties().GetFields()["battery.min"].GetNumberValue() != 20 {
		t.Errorf("requirements not carried: %v", out.GetRequirements())
	}
	if out.GetSkillId() != "mission" || out.GetProvider() != "Zequent Simulator" ||
		out.GetSource() != devicecontrolpb.CapabilitySourceProto_CAPABILITY_SOURCE_EDGE_ADAPTER {
		t.Errorf("skill/source/provider not carried: %v %v %v",
			out.GetSkillId(), out.GetSource(), out.GetProvider())
	}
}

// Every 2.0.0 field is optional: an adapter that predates the contract must still produce exactly
// the proto it did before, with each new field left at its unset wire state.
func TestApplyCommandContractLeavesUnsetFieldsAlone(t *testing.T) {
	out := &devicecontrolpb.Capability{}
	testServer().applyCommandContract(&domains.Capability{Command: "takeOff"}, out)

	if out.InputSchema != nil || out.OutputSchema != nil || out.Requirements != nil ||
		out.SkillId != nil || out.Source != nil || out.Provider != nil ||
		len(out.Errors) != 0 || len(out.Events) != 0 {
		t.Errorf("a contract-less capability should set nothing: %v", out)
	}
}

// One unencodable schema must cost only that schema, not the whole device's capability list.
func TestApplyCommandContractDropsUnencodableSchema(t *testing.T) {
	out := &devicecontrolpb.Capability{}
	testServer().applyCommandContract(&domains.Capability{
		Command:      "broken",
		InputSchema:  map[string]any{"bad": make(chan int)},
		OutputSchema: map[string]any{"type": "object"},
	}, out)

	if out.InputSchema != nil {
		t.Error("an unencodable input schema should be dropped")
	}
	if out.GetOutputSchema() == nil {
		t.Error("a valid sibling schema should survive the dropped one")
	}
}
