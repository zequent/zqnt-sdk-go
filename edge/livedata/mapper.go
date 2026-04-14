package livedata

import (
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"

	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Mapper converts TelemetryRequestData domain objects to ProduceTelemetryRequest protos.
type Mapper struct{}

func (m *Mapper) Map(data *domains.TelemetryRequestData) *zqntpb.ProduceTelemetryRequest {
	if data == nil {
		return nil
	}

	base := &zqntpb.RequestBase{
		Tid:       data.TID,
		Sn:        data.SN,
		Timestamp: protohelpers.Now(),
	}

	req := &zqntpb.ProduceTelemetryRequest{Base: base}

	switch data.Type {
	case domains.TelemetryTypeAsset:
		req.Type = zqntpb.LiveDataType_ASSET_TELEMETRY
		if data.AssetTelemetry != nil {
			req.Telemetry = &zqntpb.ProduceTelemetryRequest_AssetTelemetry{
				AssetTelemetry: m.mapAssetTelemetry(data.AssetTelemetry),
			}
		}
	case domains.TelemetryTypeSubAsset:
		req.Type = zqntpb.LiveDataType_SUBASSET_TELEMETRY
		if data.SubAssetTelemetry != nil {
			req.Telemetry = &zqntpb.ProduceTelemetryRequest_SubAssetTelemetry{
				SubAssetTelemetry: m.mapSubAssetTelemetry(data.SubAssetTelemetry),
			}
		}
	}

	return req
}

func (m *Mapper) mapAssetTelemetry(d *domains.AssetTelemetryData) *zqntpb.AssetTelemetry {
	if d == nil {
		return nil
	}
	t := &zqntpb.AssetTelemetry{
		Id:        d.ID,
		Timestamp: timestamppb.New(d.Timestamp),
	}
	t.Latitude = d.Latitude
	t.Longitude = d.Longitude
	t.AbsoluteAltitude = d.AbsoluteAltitude
	t.RelativeAltitude = d.RelativeAltitude
	t.EnvironmentTemp = d.EnvironmentTemp
	t.InsideTemp = d.InsideTemp
	t.Humidity = d.Humidity
	t.SubAssetAtHome = d.SubAssetAtHome
	t.SubAssetCharging = d.SubAssetCharging
	t.SubAssetPercentage = d.SubAssetPercentage
	t.Heading = d.Heading
	t.DebugModeOpen = d.DebugModeOpen
	t.HasActiveManualControlSession = d.HasActiveManualControl
	t.WorkingVoltage = d.WorkingVoltage
	t.WorkingCurrent = d.WorkingCurrent
	t.SupplyVoltage = d.SupplyVoltage
	t.WindSpeed = d.WindSpeed
	t.PositionValid = d.PositionValid

	if d.SubAssetInformation != nil {
		t.SubAssetInformation = &zqntpb.AssetTelemetry_AssetSubAssetInformation{
			Sn:     d.SubAssetInformation.SN,
			Model:  d.SubAssetInformation.Model,
			Paired: d.SubAssetInformation.Paired,
			Online: d.SubAssetInformation.Online,
		}
	}
	if d.NetworkInformation != nil {
		ni := &zqntpb.AssetTelemetry_AssetNetworkInformation{
			Rate: d.NetworkInformation.Rate,
		}
		t.NetworkInformation = ni
	}
	if d.AirConditioner != nil {
		t.AirConditioner = &zqntpb.AssetTelemetry_AssetAirConditioner{
			SwitchTime: d.AirConditioner.SwitchTime,
		}
	}
	return t
}

func (m *Mapper) mapSubAssetTelemetry(d *domains.SubAssetTelemetryData) *zqntpb.SubAssetTelemetry {
	if d == nil {
		return nil
	}
	t := &zqntpb.SubAssetTelemetry{
		Id:        d.ID,
		Timestamp: timestamppb.New(d.Timestamp),
	}
	t.Latitude = d.Latitude
	t.Longitude = d.Longitude
	t.AbsoluteAltitude = d.AbsoluteAltitude
	t.RelativeAltitude = d.RelativeAltitude
	t.HorizontalSpeed = d.HorizontalSpeed
	t.VerticalSpeed = d.VerticalSpeed
	t.WindSpeed = d.WindSpeed
	t.WindDirection = d.WindDirection
	t.Heading = d.Heading
	t.Gear = d.Gear
	t.HeightLimit = d.HeightLimit
	t.HomeDistance = d.HomeDistance
	t.TotalMovementDistance = d.TotalMovementDistance
	t.TotalMovementTime = d.TotalMovementTime
	t.Country = d.Country

	if d.BatteryInformation != nil {
		t.BatteryInformation = &zqntpb.SubAssetTelemetry_SubAssetBatteryInformation{
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

func (m *Mapper) mapPayloadTelemetry(d *domains.PayloadTelemetryData) *zqntpb.PayloadTelemetry {
	if d == nil {
		return nil
	}
	p := &zqntpb.PayloadTelemetry{
		Id:        d.ID,
		Name:      d.Name,
		Timestamp: timestamppb.New(d.Timestamp),
	}
	if d.CameraData != nil {
		p.CameraData = &zqntpb.PayloadTelemetry_CameraData{
			CurrentLens: d.CameraData.CurrentLens,
			GimbalPitch: d.CameraData.GimbalPitch,
			GimbalYaw:   d.CameraData.GimbalYaw,
			GimbalRoll:  d.CameraData.GimbalRoll,
			ZoomFactor:  d.CameraData.ZoomFactor,
		}
	}
	if d.RangeFinderData != nil {
		p.RangeFinderData = &zqntpb.PayloadTelemetry_RangeFinderData{
			TargetLatitude:  d.RangeFinderData.TargetLatitude,
			TargetLongitude: d.RangeFinderData.TargetLongitude,
			TargetDistance:  d.RangeFinderData.TargetDistance,
			TargetAltitude:  d.RangeFinderData.TargetAltitude,
		}
	}
	if d.SensorData != nil {
		p.SensorData = &zqntpb.PayloadTelemetry_SensorData{
			TargetTemperature: d.SensorData.TargetTemperature,
		}
	}
	return p
}
