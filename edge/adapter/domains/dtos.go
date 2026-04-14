package domains

import "time"

// AssetDTO is the domain representation of an asset (dock, camera, etc.).
type AssetDTO struct {
	ID                   string
	SN                   string
	Name                 string
	Type                 string
	Vendor               string
	Connection           string
	ConnectionString     *string
	Model                string
	Port                 *int32
	LiveStreamServer     *string
	ExternalDeviceType   *string
	ExternalDeviceSubType *string
	Organization         string
	Online               bool
	SubAsset             *SubAssetDTO
	ExternalID           *string
	StreamType           string
}

// SubAssetDTO is the domain representation of a sub-asset (drone).
type SubAssetDTO struct {
	ID                   string
	SN                   string
	Name                 string
	Type                 string
	Vendor               string
	Connection           string
	ConnectionString     *string
	Model                string
	Port                 *int32
	LiveStreamServer     *string
	ExternalDeviceType   *string
	ExternalDeviceSubType *string
	Organization         string
	Online               bool
	ExternalID           *string
	StreamType           string
	StreamURLPredefined  *bool
}

// OrganizationDTO is the domain representation of an organization.
type OrganizationDTO struct {
	ID          string
	Name        string
	Description string
	Assets      []string
}

// MissionDTO is the domain representation of a mission.
type MissionDTO struct {
	ID             *string
	Name           string
	Description    string
	Tasks          []TaskDTO
	Status         string
	Type           string
	GeoJSON        *string
	StartDate      *time.Time
	EndDate        *time.Time
	AssignedAssets []string
	CreatedAt      *time.Time
	ModifiedAt     *time.Time
	UpdatedUser    *string
}

// TaskDTO is the domain representation of a task.
type TaskDTO struct {
	ID              *string
	MissionID       *string
	Name            *string
	Description     *string
	TaskType        *string
	Status          string
	AssetID         *string
	SNNumber        *string
	CurrentProgress *int32
	CurrentStep     *string
	BreakReason     *string
	CreatedAt       *time.Time
	ModifiedAt      *time.Time
}

// SchedulerDTO is the domain representation of a scheduler.
type SchedulerDTO struct {
	ID             *string
	Name           string
	MissionID      *string
	TaskID         *string
	CronExpression string
	Active         *bool
	Type           string
	ClientTimeZone *string
	CreatedAt      *time.Time
	ModifiedAt     *time.Time
}
