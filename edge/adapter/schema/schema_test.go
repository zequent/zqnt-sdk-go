package schema

import (
	"errors"
	"strings"
	"testing"
)

var goTo = map[string]any{
	"type":     "object",
	"required": []any{"latitude", "longitude"},
	"properties": map[string]any{
		"latitude":  map[string]any{"type": "number", "minimum": -90, "maximum": 90},
		"longitude": map[string]any{"type": "number"},
		"altitude":  map[string]any{"type": "number"},
		"speed":     map[string]any{"type": "integer"},
		"waypoints": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "properties": map[string]any{"hold": map[string]any{"type": "integer"}}}},
	},
}

func TestPrepareCoercesWholeNumbersOnlyWhereTheSchemaSaysInteger(t *testing.T) {
	s, err := Compile(goTo)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Prepare(map[string]any{"latitude": 52.0, "longitude": 13.4, "speed": 12.0,
		"waypoints": []any{map[string]any{"hold": 3.0}}})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := out["speed"].(int64); !ok || v != 12 {
		t.Errorf("speed = %#v, want int64(12)", out["speed"])
	}
	if v, ok := out["latitude"].(float64); !ok || v != 52 {
		t.Errorf("latitude = %#v, want float64", out["latitude"])
	}
	hold := out["waypoints"].([]any)[0].(map[string]any)["hold"]
	if v, ok := hold.(int64); !ok || v != 3 {
		t.Errorf("nested hold = %#v, want int64(3)", hold)
	}
}

func TestPrepareNamesEveryProblem(t *testing.T) {
	s, _ := Compile(goTo)
	_, err := s.Prepare(map[string]any{"latitude": 120.0, "speed": 1.5})
	var invalid *InvalidParamsError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want InvalidParamsError", err)
	}
	msg := err.Error()
	for _, want := range []string{"longitude", "latitude", "speed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %s", msg, want)
		}
	}
}

func TestNoSchemaAcceptsAnything(t *testing.T) {
	s, err := Compile(nil)
	if err != nil || s != nil {
		t.Fatalf("Compile(nil) = %v, %v", s, err)
	}
	out, err := s.Prepare(map[string]any{"x": "y"})
	if err != nil || out["x"] != "y" {
		t.Fatalf("Prepare = %v, %v", out, err)
	}
}

func TestCompileRefusesABrokenSchema(t *testing.T) {
	if _, err := Compile(map[string]any{"type": 12}); err == nil {
		t.Fatal("a schema whose type is a number must not compile")
	}
}

func TestExampleSatisfiesTheSchema(t *testing.T) {
	doc := map[string]any{
		"type":     "object",
		"required": []any{"latitude", "longitude", "mode", "name", "count", "tags"},
		"properties": map[string]any{
			"latitude":  map[string]any{"type": "number", "minimum": -90, "maximum": 90},
			"longitude": map[string]any{"type": "number", "exclusiveMinimum": 5},
			"mode":      map[string]any{"type": "string", "enum": []any{"AUTO", "MANUAL"}},
			"name":      map[string]any{"type": "string", "minLength": 2},
			"count":     map[string]any{"type": "integer", "default": 3},
			"tags":      map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}},
			"optional":  map[string]any{"type": "boolean"},
		},
	}
	s, err := Compile(doc)
	if err != nil {
		t.Fatal(err)
	}
	ex := Example(doc)
	if _, err := s.Prepare(ex); err != nil {
		t.Fatalf("example %v does not validate: %v", ex, err)
	}
	if _, ok := ex["optional"]; ok {
		t.Error("optional properties are left out")
	}
}
