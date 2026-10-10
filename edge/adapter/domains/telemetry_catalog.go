package domains

import "github.com/zequent/zqnt-utils-golang/v2/telemetry"

// StandardTelemetryField describes a key of the platform's telemetry catalog (the keys every
// language maps to and from v2 telemetry), or reports false for a key outside it. Declaring
// catalog keys this way keeps their type, unit and allowed values the platform's.
func StandardTelemetryField(key string) (TelemetryField, bool) {
	f := telemetry.Field(key)
	if f == nil {
		return TelemetryField{}, false
	}
	return TelemetryField{
		Key:           f.GetKey(),
		Type:          TelemetryValueType(f.GetType()),
		Unit:          f.GetUnit(),
		Description:   f.GetDescription(),
		AllowedValues: f.GetAllowedValues(),
	}, true
}
