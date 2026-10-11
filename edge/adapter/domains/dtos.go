package domains

import "time"

// AssetDTO is the domain representation of an asset (dock, camera, etc.).
//
// Reshaped 2026-09-02 to match the current AssetProtoDTO (asset.proto): ConnectionString/Port/
// LiveStreamServer/StreamType/Online/SubAsset (singular) no longer exist on the wire message --
// replaced by SystemConnectionString, LiveStreamPushUrl/LiveStreamPullUrl, SubAssets (repeated),
// and Payloads; Online moved out of the DTO entirely (asset online status is tracked via Redis/
// monitoring elsewhere on the platform, not part of the system-of-record Asset message anymore).
// Most fields are now pointers, mirroring the proto's own `optional` markers.
type AssetDTO struct {
	ID                     *string
	SN                     *string
	Name                   *string
	Type                   string
	Vendor                 string
	Connection             string
	SystemConnectionString *string
	Model                  *string
	ExternalDeviceType     *string
	ExternalDeviceSubType  *string
	Organization           *string
	ExternalID             *string
	Payloads               []AssetPayloadDTO
	SubAssets              []SubAssetDTO
	ModifiedFrom           *string
	LiveStreamPushURL      *string
	LiveStreamPullURL      *string
	CreatedAt              *time.Time
	ModifiedAt             *time.Time
}

// SubAssetDTO is the domain representation of a sub-asset (drone).
type SubAssetDTO struct {
	ID                     *string
	SN                     *string
	Name                   *string
	Type                   string
	Vendor                 string
	Connection             string
	SystemConnectionString *string
	Model                  *string
	ExternalDeviceType     *string
	ExternalDeviceSubType  *string
	ExternalID             *string
	StreamURLPredefined    *bool
	Payloads               []AssetPayloadDTO
	ModifiedFrom           *string
	LiveStreamPushURL      *string
	LiveStreamPullURL      *string
	CreatedAt              *time.Time
	ModifiedAt             *time.Time
}

// AssetPayloadDTO is the domain representation of a payload (camera, sensor, etc.) attached to an
// asset or sub-asset. New in the current schema -- the old proto had no equivalent.
type AssetPayloadDTO struct {
	ID              *string
	ExternalID      *string
	ExternalType    *string
	SlotIndex       *int32
	Name            *string
	SerialNumber    *string
	Kind            *string
	Vendor          *string
	Model           *string
	FirmwareVersion *string
	LibraryVersion  *string
	Active          bool
	PayloadRef      *string
	LastSeenAt      *time.Time
	CreatedAt       *time.Time
	ModifiedAt      *time.Time
}

// OrganizationDTO is the domain representation of an organization.
type OrganizationDTO struct {
	ID          string
	Name        string
	Description string
	Assets      []string
}

// SchedulerDTO is the domain representation of a scheduler.
//
// Mission-free as of the 2.0.0 contract: MissionID/TaskID are gone (reserved 3, 4 on
// SchedulerProtoDTO) along with Mission/Task themselves, and a schedule now names what to run
// directly. Exactly one of CommandID or ApplicationID+SkillID is expected on a new schedule --
// a single command, or one skill of a published Application.
type SchedulerDTO struct {
	ID             *string
	Name           string
	CronExpression string
	Type           string
	Active         *bool
	ClientTimeZone *string
	CreatedAt      *time.Time
	ModifiedAt     *time.Time
	// AssetSN is the asset the schedule fires against.
	AssetSN *string
	// CommandID is a single command id (e.g. "flight.return_to_home"), for a schedule that runs
	// one command rather than an Application skill.
	CommandID *string
	// ApplicationID/SkillID name one skill of a published Application instead.
	ApplicationID *string
	SkillID       *string
	// ExecutionParameters are the params handed to the command/skill, JSON-shaped
	// (google.protobuf.Struct on the wire).
	ExecutionParameters map[string]any
	// AutoStart runs the resulting SkillExecution immediately instead of leaving it created.
	AutoStart *bool
}
