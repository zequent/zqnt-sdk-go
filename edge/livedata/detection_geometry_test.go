package livedata

import (
	"math"
	"testing"

	"github.com/zequent/zqnt-edge-sdk-go/v3/adapter/domains"
)

func TestRadarGeometryReachesV3AndV2(t *testing.T) {
	batch := &domains.DetectionBatch{SN: "RADAR-1", Detections: []domains.DetectionResult{{
		ObjectID: "track-7", ObjectType: "UAV", Confidence: 0.8,
		Position:    &domains.GeoPoint{Lat: 52.5, Lon: 13.4, Alt: ptr(float64(120))},
		RangeMeters: ptr(float64(900)), BearingDegrees: ptr(float64(45)), ElevationDegrees: ptr(float64(3)),
		Speed: ptr(float64(12)), HeadingDegrees: ptr(float64(270)),
	}}}

	v3 := detectionBatchV3(batch).GetDetections()[0]
	if v3.GetRangeMeters() != 900 || v3.GetBearingDegrees() != 45 || v3.GetElevationDegrees() != 3 ||
		v3.GetSpeed() != 12 || v3.GetHeadingDegrees() != 270 || v3.GetPosition().GetLatitude() != 52.5 {
		t.Fatalf("v3 detection = %v", v3)
	}

	v2 := detectionBatchV2(batch).GetDetections()[0].GetPosition()
	if v2.GetLatitude() != 52.5 || v2.GetAltitude() != 120 || v2.GetRangeM() != 900 || v2.GetBearingDeg() != 45 ||
		v2.GetElevationDeg() != 3 || v2.GetSpeedMps() != 12 || v2.GetHeadingDeg() != 270 {
		t.Fatalf("v2 position = %v", v2)
	}
}

func TestRangeAndBearingWithoutAPositionLeaveV2CoordinatesNaN(t *testing.T) {
	batch := &domains.DetectionBatch{SN: "RADAR-1", Detections: []domains.DetectionResult{{
		ObjectType: "UAV", RangeMeters: ptr(float64(900)), BearingDegrees: ptr(float64(45)),
	}}}

	if detectionBatchV3(batch).GetDetections()[0].GetPosition() != nil {
		t.Fatal("v3 detection without a position got one")
	}
	v2 := detectionBatchV2(batch).GetDetections()[0].GetPosition()
	if !math.IsNaN(v2.GetLatitude()) || !math.IsNaN(v2.GetLongitude()) || v2.GetRangeM() != 900 {
		t.Fatalf("v2 position = %v", v2)
	}
}

func TestACameraDetectionHasNoV2Position(t *testing.T) {
	batch := &domains.DetectionBatch{SN: "CAM-1", Detections: []domains.DetectionResult{{ObjectType: "person"}}}
	if detectionBatchV2(batch).GetDetections()[0].GetPosition() != nil {
		t.Fatal("camera detection got a v2 position")
	}
}
