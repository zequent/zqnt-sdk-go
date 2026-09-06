package domains

import "time"

// CapabilityTargetType identifies which runtime object a Capability's Command actually runs
// against -- mirrors device-control-contracts.proto's CapabilityTargetType. Left at its zero
// value (CapabilityTargetUnspecified) unless a Capability sets it explicitly: mission-autonomy's
// own capability lookup (MissionAutonomyGrpcService.startDynamicCommand's sameTarget check)
// requires an exact match against the caller's own configured target, and a device-level command
// (the common case -- see edge-dji's own CAPABILITY_TARGET_TYPE_ASSET convention for its built-in
// commands) needs CapabilityTargetAsset, not the unspecified default, to ever match.
type CapabilityTargetType int

const (
	CapabilityTargetUnspecified CapabilityTargetType = iota
	CapabilityTargetAsset
	CapabilityTargetSubAsset
	CapabilityTargetPayload
	CapabilityTargetComponent
)

// Capability describes a single command that an asset may or may not support.
type Capability struct {
	Command           string
	Description       string
	Available         bool
	UnavailableReason *string
	Metadata          map[string]string
	// TargetType/TargetRef -- see CapabilityTargetType's own doc comment for why these matter.
	// TargetRef is empty for CapabilityTargetAsset (a device-level command has nothing else to
	// name); set it for SubAsset/Payload/Component targets, the same way CapabilityTarget's own
	// "empty only when type is ASSET" field comment requires on the wire.
	TargetType CapabilityTargetType
	TargetRef  *string
	// SchemaVersion is this command's own input/output contract version, surfaced so a caller
	// that pins an expected version (Task.dynamicCommandConfig.expectedSchemaVersion) can detect
	// drift -- see startDynamicCommand's schema-version check. Optional: leave empty if the
	// command's shape has never needed versioning.
	SchemaVersion string
}

// CurrentCapabilities is the response from GetCapabilities.
type CurrentCapabilities struct {
	SN           string
	AssetType    string
	Capabilities []Capability
	Timestamp    time.Time
}

// EmptyCapabilities returns an empty CurrentCapabilities for a given serial number.
// Used by the default UnimplementedEdgeAdapter.GetCapabilities implementation.
func EmptyCapabilities(sn string) *CurrentCapabilities {
	return &CurrentCapabilities{
		SN:           sn,
		Capabilities: []Capability{},
		Timestamp:    time.Now(),
	}
}
