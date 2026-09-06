package adaptergrpc

import (
	"testing"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	devicecontrolpb "github.com/zequent/zqnt-utils-golang/gen/devicecontrol/contracts/proto"
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
