package schema

import "strings"

// Example builds params that satisfy a simple input schema: every required property gets its
// default, const, first enum value or first example, else the smallest value its type and bounds
// allow. Good enough to call a command in a test; not a general schema solver.
func Example(doc map[string]any) map[string]any {
	v, _ := example(doc).(map[string]any)
	if v == nil {
		v = map[string]any{}
	}
	return v
}

func example(node any) any {
	s, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"default", "const"} {
		if v, ok := s[key]; ok {
			return v
		}
	}
	for _, key := range []string{"enum", "examples"} {
		if list, ok := s[key].([]any); ok && len(list) > 0 {
			return list[0]
		}
	}
	switch {
	case declares(s, "object") || s["properties"] != nil:
		out := map[string]any{}
		props, _ := s["properties"].(map[string]any)
		for _, name := range required(s) {
			out[name] = example(props[name])
		}
		return out
	case declares(s, "integer"), declares(s, "number"):
		return lowest(s)
	case declares(s, "string"):
		n, _ := numberOf(s["minLength"])
		return strings.Repeat("x", int(n))
	case declares(s, "boolean"):
		return false
	case declares(s, "array"):
		out := []any{}
		n, _ := numberOf(s["minItems"])
		for range int(n) {
			out = append(out, example(s["items"]))
		}
		return out
	}
	return nil
}

func required(s map[string]any) []string {
	var out []string
	switch r := s["required"].(type) {
	case []any:
		for _, v := range r {
			if name, ok := v.(string); ok {
				out = append(out, name)
			}
		}
	case []string:
		out = r
	}
	return out
}

func lowest(s map[string]any) float64 {
	if v, ok := numberOf(s["minimum"]); ok {
		return v
	}
	if v, ok := numberOf(s["exclusiveMinimum"]); ok {
		return v + 1
	}
	if v, ok := numberOf(s["maximum"]); ok && v < 0 {
		return v
	}
	return 0
}

func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
