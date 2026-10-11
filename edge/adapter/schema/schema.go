// Package schema validates command params against a command's input JSON Schema.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// InvalidParamsCode is the error code of a command refused because its params do not match the
// command's input schema.
const InvalidParamsCode = "command.invalid_params"

// Schema is a compiled JSON Schema.
type Schema struct {
	doc      map[string]any
	compiled *jsonschema.Schema
}

// Compile parses a JSON Schema given as a Go map. A nil or empty map compiles to nil: no schema,
// nothing to check.
func Compile(doc map[string]any) (*Schema, error) {
	if len(doc) == 0 {
		return nil, nil
	}
	normalized, err := normalize(doc)
	if err != nil {
		return nil, fmt.Errorf("schema is not JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("command.json", normalized); err != nil {
		return nil, err
	}
	compiled, err := c.Compile("command.json")
	if err != nil {
		return nil, err
	}
	return &Schema{doc: doc, compiled: compiled}, nil
}

// InvalidParamsError says which params are wrong and why.
type InvalidParamsError struct {
	Problems []string
}

func (e *InvalidParamsError) Error() string {
	return "invalid params: " + strings.Join(e.Problems, "; ")
}

// Prepare validates params and returns them with numbers coerced to what the schema declares:
// google.protobuf.Struct carries every number as a double, so a whole number under an "integer"
// property becomes an int64. A nil schema returns params unchanged. A mismatch is an
// *InvalidParamsError.
func (s *Schema) Prepare(params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	if s == nil {
		return params, nil
	}
	instance, err := normalize(params)
	if err != nil {
		return nil, &InvalidParamsError{Problems: []string{err.Error()}}
	}
	if err := s.compiled.Validate(instance); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return nil, &InvalidParamsError{Problems: problems(ve)}
		}
		return nil, &InvalidParamsError{Problems: []string{err.Error()}}
	}
	coerced, _ := coerce(s.doc, params).(map[string]any)
	return coerced, nil
}

func normalize(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
}

func problems(ve *jsonschema.ValidationError) []string {
	var out []string
	for _, unit := range ve.BasicOutput().Errors {
		if unit.Error == nil {
			continue
		}
		msg := unit.Error.String()
		if strings.HasPrefix(msg, "validation failed") {
			continue
		}
		where := strings.TrimPrefix(unit.InstanceLocation, "/")
		if where == "" {
			out = append(out, msg)
		} else {
			out = append(out, where+": "+msg)
		}
	}
	if len(out) == 0 {
		out = []string{ve.Error()}
	}
	sort.Strings(out)
	return out
}

func coerce(doc any, value any) any {
	s, ok := doc.(map[string]any)
	if !ok {
		return value
	}
	switch v := value.(type) {
	case float64:
		if declares(s, "integer") && !declares(s, "number") && v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v)
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = coerce(props[k], item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = coerce(s["items"], item)
		}
		return out
	}
	return value
}

func declares(s map[string]any, typ string) bool {
	switch t := s["type"].(type) {
	case string:
		return t == typ
	case []any:
		for _, x := range t {
			if x == typ {
				return true
			}
		}
	case []string:
		for _, x := range t {
			if x == typ {
				return true
			}
		}
	}
	return false
}
