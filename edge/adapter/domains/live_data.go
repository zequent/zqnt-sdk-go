package domains

import "time"

// GeoPoint is a position; Alt is in metres, its reference stated by the field that uses it.
type GeoPoint struct {
	Lat float64
	Lon float64
	Alt *float64
}

// TelemetrySample is one v3 telemetry sample: the fields every asset shares, plus Details under
// the keys the asset declares in CurrentCapabilities.TelemetryFields.
type TelemetrySample struct {
	SN         string
	ObservedAt time.Time
	// Position altitude is above sea level.
	Position *GeoPoint
	// RelativeAltitude is metres above the takeoff point.
	RelativeAltitude *float64
	// HeadingDegrees is clockwise from true north.
	HeadingDegrees  *float64
	HorizontalSpeed *float64
	// VerticalSpeed is m/s, positive up.
	VerticalSpeed  *float64
	BatteryPercent *float64
	Details        map[string]any
}

// DetectionBatch is what the asset's detector saw in one frame.
type DetectionBatch struct {
	SN         string
	ObservedAt time.Time
	StreamURL  string
	Detections []DetectionResult
}

// AlertSeverity ranks an Alert.
type AlertSeverity int

const (
	AlertSeverityUnspecified AlertSeverity = iota
	AlertSeverityInfo
	AlertSeverityWarning
	AlertSeverityCritical
)

// Alert is something the asset reports on its own, e.g. a dock's rain warning or a motor fault.
type Alert struct {
	SN         string
	OccurredAt time.Time
	Severity   AlertSeverity
	// Code is stable and machine-readable, e.g. "dock.rain".
	Code    string
	Message string
	Details map[string]any
}
