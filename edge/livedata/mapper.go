package livedata

import (
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	commonpb "github.com/Zequent/zqnt-edge-sdk-go/gen/common/proto"
	livedatapb "github.com/Zequent/zqnt-edge-sdk-go/gen/livedata/proto"

	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Mapper converts TelemetryRequestData domain objects to ProduceTelemetryRequest protos.
type Mapper struct{}

func (m *Mapper) Map(data *domains.TelemetryRequestData) *livedatapb.ProduceTelemetryRequest {
	if data == nil {
		return nil
	}

	base := &commonpb.RequestBase{
		Tid:       data.TID,
		Sn:        data.SN,
		Timestamp: protohelpers.Now(),
	}

	telemetry := &livedatapb.Telemetry{
		Sn:        data.SN,
		Timestamp: timestamppb.Now(),
	}

	switch data.Type {
	case domains.TelemetryTypeAsset:
		if d := data.AssetTelemetry; d != nil {
			telemetry.Id = d.ID
			telemetry.Timestamp = timestamppb.New(d.Timestamp)
			telemetry.Latitude = f32to64(d.Latitude)
			telemetry.Longitude = f32to64(d.Longitude)
			telemetry.AbsoluteAltitude = d.AbsoluteAltitude
			telemetry.RelativeAltitude = d.RelativeAltitude
			telemetry.WindSpeed = d.WindSpeed
			telemetry.Heading = d.Heading
			telemetry.Source = &livedatapb.Telemetry_Asset{Asset: m.mapAssetTelemetry(d)}
		}
	case domains.TelemetryTypeSubAsset:
		if d := data.SubAssetTelemetry; d != nil {
			telemetry.Id = d.ID
			telemetry.Timestamp = timestamppb.New(d.Timestamp)
			telemetry.Latitude = f32to64(d.Latitude)
			telemetry.Longitude = f32to64(d.Longitude)
			telemetry.AbsoluteAltitude = d.AbsoluteAltitude
			telemetry.RelativeAltitude = d.RelativeAltitude
			telemetry.WindSpeed = d.WindSpeed
			telemetry.Heading = d.Heading
			telemetry.Source = &livedatapb.Telemetry_SubAsset{SubAsset: m.mapSubAssetTelemetry(d)}
		}
	}

	return &livedatapb.ProduceTelemetryRequest{
		Base:      base,
		Telemetry: &livedatapb.ProduceTelemetryRequest_Data{Data: telemetry},
	}
}

func (m *Mapper) mapAssetTelemetry(d *domains.AssetTelemetryData) *livedatapb.AssetTelemetryDetails {
	if d == nil {
		return nil
	}
	t := &livedatapb.AssetTelemetryDetails{
		EnvironmentTemp:               d.EnvironmentTemp,
		InsideTemp:                    d.InsideTemp,
		Humidity:                      d.Humidity,
		Mode:                          enumPtr[commonpb.AssetMode](commonpb.AssetMode_value, d.Mode),
		Rainfall:                      enumPtr[commonpb.RainfallEnum](commonpb.RainfallEnum_value, d.Rainfall),
		SubAssetAtHome:                d.SubAssetAtHome,
		SubAssetCharging:              d.SubAssetCharging,
		SubAssetPercentage:            d.SubAssetPercentage,
		DebugModeOpen:                 d.DebugModeOpen,
		HasActiveManualControlSession: d.HasActiveManualControl,
		CoverState:                    enumPtr[commonpb.AssetCoverStateEnum](commonpb.AssetCoverStateEnum_value, d.CoverState),
		WorkingVoltage:                d.WorkingVoltage,
		WorkingCurrent:                d.WorkingCurrent,
		SupplyVoltage:                 d.SupplyVoltage,
		PositionValid:                 d.PositionValid,
		ManualControlState:            enumPtr[commonpb.ManualControlStateEnum](commonpb.ManualControlStateEnum_value, d.ManualControlState),
	}

	if d.SubAssetInformation != nil {
		t.SubAssetInformation = &livedatapb.AssetTelemetryDetails_AssetSubAssetInformation{
			Sn:     d.SubAssetInformation.SN,
			Model:  d.SubAssetInformation.Model,
			Paired: d.SubAssetInformation.Paired,
			Online: d.SubAssetInformation.Online,
		}
	}
	if d.NetworkInformation != nil {
		t.NetworkInformation = &livedatapb.AssetTelemetryDetails_AssetNetworkInformation{
			Type:    enumPtr[commonpb.NetworkTypeEnum](commonpb.NetworkTypeEnum_value, d.NetworkInformation.Type),
			Rate:    d.NetworkInformation.Rate,
			Quality: enumPtr[commonpb.NetworkStateQualityEnum](commonpb.NetworkStateQualityEnum_value, d.NetworkInformation.Quality),
		}
	}
	if d.AirConditioner != nil {
		t.AirConditioner = &livedatapb.AssetTelemetryDetails_AssetAirConditioner{
			State:      enumPtr[commonpb.AssetAirConditionerStateEnum](commonpb.AssetAirConditionerStateEnum_value, d.AirConditioner.State),
			SwitchTime: d.AirConditioner.SwitchTime,
		}
	}
	return t
}

func (m *Mapper) mapSubAssetTelemetry(d *domains.SubAssetTelemetryData) *livedatapb.SubAssetTelemetryDetails {
	if d == nil {
		return nil
	}
	t := &livedatapb.SubAssetTelemetryDetails{
		HorizontalSpeed:       d.HorizontalSpeed,
		VerticalSpeed:         d.VerticalSpeed,
		WindDirection:         d.WindDirection,
		Gear:                  d.Gear,
		HeightLimit:           d.HeightLimit,
		HomeDistance:          d.HomeDistance,
		TotalMovementDistance: d.TotalMovementDistance,
		TotalMovementTime:     d.TotalMovementTime,
		Mode:                  enumPtr[commonpb.SubAssetMode](commonpb.SubAssetMode_value, d.Mode),
		Country:               d.Country,
	}

	if d.BatteryInformation != nil {
		t.BatteryInformation = &livedatapb.SubAssetTelemetryDetails_SubAssetBatteryInformation{
			Percentage:        d.BatteryInformation.Percentage,
			RemainingTime:     d.BatteryInformation.RemainingTime,
			ReturnToHomePower: d.BatteryInformation.ReturnHomePower,
		}
	}
	if d.PayloadTelemetry != nil {
		t.PayloadTelemetry = m.mapPayloadTelemetry(d.PayloadTelemetry)
	}
	return t
}

func (m *Mapper) mapPayloadTelemetry(d *domains.PayloadTelemetryData) *livedatapb.PayloadTelemetry {
	if d == nil {
		return nil
	}
	p := &livedatapb.PayloadTelemetry{
		Id:        d.ID,
		Name:      d.Name,
		Timestamp: timestamppb.New(d.Timestamp),
	}
	if d.CameraData != nil {
		p.CameraData = &livedatapb.PayloadTelemetry_CameraData{
			CurrentLens: d.CameraData.CurrentLens,
			GimbalPitch: d.CameraData.GimbalPitch,
			GimbalYaw:   d.CameraData.GimbalYaw,
			GimbalRoll:  d.CameraData.GimbalRoll,
			ZoomFactor:  d.CameraData.ZoomFactor,
		}
	}
	if d.RangeFinderData != nil {
		p.RangeFinderData = &livedatapb.PayloadTelemetry_RangeFinderData{
			TargetLatitude:  f32to64(d.RangeFinderData.TargetLatitude),
			TargetLongitude: f32to64(d.RangeFinderData.TargetLongitude),
			TargetDistance:  d.RangeFinderData.TargetDistance,
			TargetAltitude:  d.RangeFinderData.TargetAltitude,
		}
	}
	if d.SensorData != nil {
		p.SensorData = &livedatapb.PayloadTelemetry_SensorData{
			TargetTemperature: d.SensorData.TargetTemperature,
		}
	}
	return p
}

// f32to64 converts a *float32 to a *float64 (Telemetry.latitude/longitude and
// PayloadTelemetry.RangeFinderData's target lat/lon widened from float32 to double in the current
// schema; the domain model still carries float32, matching every other telemetry field).
func f32to64(v *float32) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

// enumPtr resolves a domain *string (the proto enum's full name, e.g. "ASSET_MODE_WORKING") to a
// pointer to the generated enum type T via protoc-gen-go's standard <Enum>_value map. Returns nil
// if the domain pointer is nil or the name isn't recognized (rather than sending a zero value,
// which is itself a meaningful enum member in every one of these enums).
func enumPtr[T ~int32](valueMap map[string]int32, s *string) *T {
	if s == nil {
		return nil
	}
	v, ok := valueMap[*s]
	if !ok {
		return nil
	}
	t := T(v)
	return &t
}
