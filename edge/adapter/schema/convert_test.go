package schema

import (
	"reflect"
	"testing"
)

var spray = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"seconds":  map[string]any{"type": "integer"},
		"pressure": map[string]any{"type": "number"},
		"dryRun":   map[string]any{"type": "boolean"},
		"nozzle":   map[string]any{"type": "string", "enum": []string{"FINE", "WIDE"}},
		"label":    map[string]any{"type": "string"},
		"waypoints": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "properties": map[string]any{"latitude": map[string]any{"type": "number"}}}},
	},
	"additionalProperties": map[string]any{"type": "boolean"},
}

func convertSpray(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	s, err := Compile(spray)
	if err != nil {
		t.Fatal(err)
	}
	return s.ConvertText(params)
}

func TestConvertTextMakesExactConversions(t *testing.T) {
	got := convertSpray(t, map[string]any{"seconds": "3.0", "pressure": "-1e3", "dryRun": " false ", "nozzle": "fine",
		"label": "1.0", "waypoints": []any{map[string]any{"latitude": "47.5"}}, "extra": "TRUE"})
	want := map[string]any{"seconds": int64(3), "pressure": -1000.0, "dryRun": false, "nozzle": "FINE",
		"label": "1.0", "waypoints": []any{map[string]any{"latitude": 47.5}}, "extra": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestConvertTextLeavesWhatDoesNotConvertExactly(t *testing.T) {
	in := map[string]any{"seconds": "3.5", "pressure": "NaN", "dryRun": "yes", "nozzle": "medium", "label": "0123"}
	if got := convertSpray(t, in); !reflect.DeepEqual(got, in) {
		t.Fatalf("got %#v", got)
	}
	for _, text := range []string{"1_000", "Infinity", "0x10", "", "1e400"} {
		if got := convertSpray(t, map[string]any{"pressure": text}); got["pressure"] != text {
			t.Errorf("%q became %#v", text, got["pressure"])
		}
	}
}

func TestConvertTextLeavesAnAmbiguousEnumValueToValidation(t *testing.T) {
	s, err := Compile(map[string]any{"type": "object", "properties": map[string]any{
		"mode": map[string]any{"type": "string", "enum": []any{"auto", "AUTO"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ConvertText(map[string]any{"mode": "Auto"}); got["mode"] != "Auto" {
		t.Fatalf("mode = %#v", got["mode"])
	}
}

func TestConvertTextWithoutASchemaChangesNothing(t *testing.T) {
	var s *Schema
	in := map[string]any{"seconds": "3"}
	if got := s.ConvertText(in); !reflect.DeepEqual(got, in) {
		t.Fatalf("got %#v", got)
	}
}
