package domains

import "time"

// TelemetryType selects whether telemetry data belongs to the dock (asset) or the drone (sub-asset).
type TelemetryType int

const (
	TelemetryTypeAsset    TelemetryType = iota // Dock / primary asset telemetry.
	TelemetryTypeSubAsset                      // Drone / sub-asset telemetry.
)

// TelemetryRequestData is the POJO-level input for ProduceTelemetryData.
// The SDK maps this to a ProduceTelemetryRequest proto before sending.
type TelemetryRequestData struct {
	TID               string
	SN                string
	AssetID           string
	Timestamp         time.Time
	Type              TelemetryType
	AssetTelemetry    *AssetTelemetryData
	SubAssetTelemetry *SubAssetTelemetryData
}

// AssetTelemetryData mirrors the AssetTelemetry proto message.
type AssetTelemetryData struct {
	ID                         string
	Timestamp                  time.Time
	Latitude                   *float32
	Longitude                  *float32
	AbsoluteAltitude           *float32
	RelativeAltitude           *float32
	EnvironmentTemp            *float32
	InsideTemp                 *float32
	Humidity                   *float32
	Mode                       *string
	Rainfall                   *string
	SubAssetInformation        *AssetSubAssetInformation
	SubAssetAtHome             *bool
	SubAssetCharging           *bool
	SubAssetPercentage         *float32
	Heading                    *float32
	DebugModeOpen              *bool
	HasActiveManualControl     *bool
	CoverState                 *string
	WorkingVoltage             *int32
	WorkingCurrent             *int32
	SupplyVoltage              *int32
	WindSpeed                  *float32
	PositionValid              *bool
	NetworkInformation         *AssetNetworkInformation
	AirConditioner             *AssetAirConditioner
	ManualControlState         *string
}

// AssetSubAssetInformation holds paired sub-asset info within asset telemetry.
type AssetSubAssetInformation struct {
	SN     *string
	Model  *string
	Paired *bool
	Online *bool
}

// AssetNetworkInformation holds network link status.
type AssetNetworkInformation struct {
	Type    *string
	Rate    *float32
	Quality *string
}

// AssetAirConditioner holds A/C state.
type AssetAirConditioner struct {
	State      *string
	SwitchTime *int32
}

// SubAssetTelemetryData mirrors the SubAssetTelemetry proto message.
type SubAssetTelemetryData struct {
	ID                    string
	Timestamp             time.Time
	Latitude              *float32
	Longitude             *float32
	AbsoluteAltitude      *float32
	RelativeAltitude      *float32
	HorizontalSpeed       *float32
	VerticalSpeed         *float32
	WindSpeed             *float32
	WindDirection         *string
	Heading               *float32
	Gear                  *int32
	PayloadTelemetry      *PayloadTelemetryData
	BatteryInformation    *SubAssetBatteryInformation
	HeightLimit           *int32
	HomeDistance          *float32
	TotalMovementDistance *float64
	TotalMovementTime     *float64
	Mode                  *string
	Country               *string
}

// SubAssetBatteryInformation holds battery state.
type SubAssetBatteryInformation struct {
	Percentage      *string
	RemainingTime   *int32
	ReturnHomePower *string
}

// PayloadTelemetryData holds payload (camera / rangefinder / sensor) data.
type PayloadTelemetryData struct {
	ID              string
	Timestamp       time.Time
	Name            string
	CameraData      *CameraData
	RangeFinderData *RangeFinderData
	SensorData      *SensorData
}

// CameraData holds camera gimbal and lens information.
type CameraData struct {
	CurrentLens  *string
	GimbalPitch  *float32
	GimbalYaw    *float32
	GimbalRoll   *float32
	ZoomFactor   *float32
}

// RangeFinderData holds rangefinder target information.
type RangeFinderData struct {
	TargetLatitude  *float32
	TargetLongitude *float32
	TargetDistance  *float32
	TargetAltitude  *float32
}

// SensorData holds thermal / IR sensor readings.
type SensorData struct {
	TargetTemperature *float32
}
