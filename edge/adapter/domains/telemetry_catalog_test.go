package domains

import (
	"slices"
	"testing"

	"github.com/zequent/zqnt-utils-golang/v2/telemetry"
)

func TestStandardTelemetryFieldComesFromTheCatalog(t *testing.T) {
	mode, ok := StandardTelemetryField(telemetry.DockMode)
	if !ok || mode.Type != TelemetryValueString || !slices.Contains(mode.AllowedValues, "WORKING") {
		t.Fatalf("dock.mode = %+v", mode)
	}
	wind, _ := StandardTelemetryField(telemetry.WindSpeed)
	if wind.Type != TelemetryValueNumber || wind.Unit != "m/s" {
		t.Fatalf("wind.speed = %+v", wind)
	}
	if _, ok := StandardTelemetryField("radar.mode"); ok {
		t.Fatal("radar.mode is not a catalog key")
	}
}
