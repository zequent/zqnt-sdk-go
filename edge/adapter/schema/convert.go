package schema

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var decimal = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)

// ConvertText converts text in params to what the schema declares, for 2.x callers that send typed
// values as text: a number or integer, true/false, an enum value in another letter case. Only
// exact conversions: "52.5200" becomes 52.52, "3.5" never becomes an integer, and "NaN", "1_000"
// or "warm" stay text, for validation to refuse. A nil schema returns params unchanged.
func (s *Schema) ConvertText(params map[string]any) map[string]any {
	if s == nil || params == nil {
		return params
	}
	converted, _ := convert(s.doc, params).(map[string]any)
	return converted
}

func convert(doc any, value any) any {
	s, ok := doc.(map[string]any)
	if !ok {
		return value
	}
	switch v := value.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		additional, _ := s["additionalProperties"].(map[string]any)
		out := make(map[string]any, len(v))
		for k, item := range v {
			sub, declared := props[k]
			if !declared && additional != nil {
				sub = additional
			}
			out[k] = convert(sub, item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = convert(s["items"], item)
		}
		return out
	case string:
		return convertString(s, v)
	}
	return value
}

func convertString(s map[string]any, value string) any {
	if match, ok := enumMatch(s, value); ok {
		return match
	}
	if _, typed := s["type"]; !typed || declares(s, "string") {
		return value
	}
	text := strings.TrimSpace(value)
	if (declares(s, "integer") || declares(s, "number")) && decimal.MatchString(text) {
		if n, err := strconv.ParseFloat(text, 64); err == nil && !math.IsInf(n, 0) {
			if declares(s, "integer") && n == math.Trunc(n) && math.Abs(n) < 1<<53 {
				return int64(n)
			}
			if declares(s, "number") {
				return n
			}
		}
	}
	if declares(s, "boolean") {
		switch strings.ToLower(text) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return value
}

// enumMatch is the one enum value that equals value apart from letter case.
func enumMatch(s map[string]any, value string) (string, bool) {
	var enum []string
	switch e := s["enum"].(type) {
	case []string:
		enum = e
	case []any:
		for _, item := range e {
			if text, ok := item.(string); ok {
				enum = append(enum, text)
			}
		}
	}
	match, found := "", 0
	for _, text := range enum {
		if text == value {
			return "", false
		}
		if strings.EqualFold(text, value) {
			match, found = text, found+1
		}
	}
	return match, found == 1
}
