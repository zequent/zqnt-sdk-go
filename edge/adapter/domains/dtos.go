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
// Reshaped 2026-09-02: Mission/Task-bound scheduling (MissionID/TaskID) no longer exists --
// schedulers now target either a bare command (CommandID) or an Application+Skill pair
// (ApplicationID/SkillID), matching the current mission-free capability-execution model. Mirrors
// edge-python-sdk's already-migrated SchedulerDTO (edge_sdk/models/scheduler.py) field-for-field.
type SchedulerDTO struct {
	ID                  *string
	Name                string
	CronExpression      string
	Type                string
	Active              *bool
	ClientTimeZone      *string
	CreatedAt           *time.Time
	ModifiedAt          *time.Time
	AssetSN             *string
	CommandID           *string
	ApplicationID       *string
	SkillID             *string
	ExecutionParameters map[string]any
	AutoStart           *bool
}
