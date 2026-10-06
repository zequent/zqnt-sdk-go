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
	// that pins an expected version can detect drift -- on the 2.0.0 contract that check lives in
	// mission-autonomy's CommandSchemaDriftChecker, which blocks an execution whose pinned version
	// has drifted BREAKING against the Skill Registry. Optional: leave empty if the command's
	// shape has never needed versioning.
	SchemaVersion string

	// --- The 2.0.0 command contract. Every field below is optional: a capability that sets none
	// of them is exactly as valid as it was before they existed, and the platform falls back to
	// sensible defaults (see each field). They exist so a device can describe a command well
	// enough to be used without probing it -- admin-console's SkillCatalogService harvests
	// precisely these off GetCapabilities into the persisted Skill Registry, and a graph editor
	// renders a node from them. ---

	// InputSchema is a JSON Schema for the params this command accepts (the params of
	// CustomCommandRequest for a custom command). The console derives its form ui:schema from
	// this, so a command with no InputSchema is one an operator can only call by guessing.
	InputSchema map[string]any
	// OutputSchema is a JSON Schema for the dynamic result the command returns.
	OutputSchema map[string]any
	// Errors are the failure codes this command's execution can end in.
	Errors []CapabilityError
	// Events are what this command (or the skill it belongs to) can emit while or after running.
	Events []CapabilityEvent
	// Requirements is what the command needs to be usable at all -- nil when it has none.
	Requirements *CapabilityRequirements
	// SkillID groups sibling commands into one logical Skill in the registry/catalog (e.g. a
	// "drone-flight" skill made of start/pause/resume/stop). Leave empty and the catalog groups
	// by the command id's own leading segment instead ("flight" from "flight.takeoff").
	SkillID string
	// Source is where the implementation comes from. An adapter's own commands are
	// CapabilitySourceEdgeAdapter.
	Source CapabilitySource
	// Provider is the human-readable origin shown in the catalog, e.g. "DJI Adapter".
	Provider string
	// Completion says whether the reply finishes the command (CompletionOnReply: a cover, a
	// light) or a later CommandExecutionEvent does (CompletionAsynchronous: take-off, go-to, return
	// home, a mission). Left unset, a result with an ExternalExecutionID waits and any other
	// success is done. Not the same as Events, which list what a Skill can react to.
	Completion CompletionMode
	// CompletionEvent names, for CompletionAsynchronous, the event in Events that reports the
	// outcome, e.g. "flight.takeoff.completed".
	CompletionEvent string
}

// CompletionMode is when a command is finished, as far as the platform can tell. The values
// match zqnt.capability.v3.CompletionMode.
type CompletionMode int

const (
	CompletionUnspecified  CompletionMode = 0
	CompletionOnReply      CompletionMode = 1
	CompletionAsynchronous CompletionMode = 2
)

// CapabilityError is one failure code a command's execution can end in -- part of the contract
// alongside the schemas, so a caller knows what can go wrong without probing the device.
type CapabilityError struct {
	Code        string
	Description string
}

// CapabilityEvent is one event a command (or its skill) can emit while or after executing.
type CapabilityEvent struct {
	Name        string
	Description string
	// PayloadSchema is a JSON Schema for the event payload.
	PayloadSchema map[string]any
}

// CapabilityRequirements is what a command needs in order to be usable at all -- declarative
// data feeding "can this Skill run on this Asset?" checks, with no enforcement implied here.
type CapabilityRequirements struct {
	AssetTypes      []string
	Payloads        []string
	RuntimeFeatures []string
	// Properties are free-form property requirements, e.g.
	// {"camera.resolution": {"supported": ["4K"]}}.
	Properties map[string]any
}

// CapabilitySource is where a Capability's implementation actually comes from -- mirrors
// device-control-contracts.proto's CapabilitySourceProto. The platform drives every source
// through the same Capability contract; this only records the origin.
type CapabilitySource int

const (
	CapabilitySourceUnspecified CapabilitySource = iota
	CapabilitySourceBuiltIn
	// CapabilitySourceEdgeAdapter is what an adapter's own commands are.
	CapabilitySourceEdgeAdapter
	CapabilitySourceRuntime
	CapabilitySourceUser
	CapabilitySourceApplication
	CapabilitySourceIntegration
	CapabilitySourceAIGenerated
)

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
