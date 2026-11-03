package tiny

import (
	_ "embed"
	"encoding/json"
	"fmt"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
)

// registryJSON is a copy of zqnt-api proto/v3/tiny_schemas.json.
//
//go:embed tiny_schemas.json
var registryJSON []byte

// Registry is the standard part of the tiny schema registry.
type Registry struct {
	Version  int              `json:"version"`
	Schemas  []RegistrySchema `json:"schemas"`
	Commands []struct {
		ID        uint16 `json:"id"`
		CommandID string `json:"command_id"`
	} `json:"commands"`
	AlarmCodes []RegistryCode `json:"alarm_codes"`
	ErrorCodes []RegistryCode `json:"error_codes"`
}

type RegistrySchema struct {
	ID        uint16 `json:"id"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
	Lane      string `json:"lane"`
	BodySizes []int  `json:"body_sizes"`
	BodyMin   int    `json:"body_min"`
	BodyMax   int    `json:"body_max"`
}

type RegistryCode struct {
	Value uint16 `json:"value"`
	Code  string `json:"code"`
}

var standard = mustRegistry()

func mustRegistry() *Registry {
	var r Registry
	if err := json.Unmarshal(registryJSON, &r); err != nil {
		panic(fmt.Sprintf("tiny: embedded schema registry: %v", err))
	}
	return &r
}

// Standard is the registry this runtime was built with.
func Standard() *Registry {
	return standard
}

// CommandSchema is the tiny schema id of a standard command; false when the command cannot be
// sent over a tiny link.
func (r *Registry) CommandSchema(commandID string) (uint16, bool) {
	for _, c := range r.Commands {
		if c.CommandID == commandID {
			return c.ID, true
		}
	}
	return 0, false
}

// AlarmCode is the dotted alert code of an ALARM frame's code value.
func (r *Registry) AlarmCode(value uint16) (string, bool) {
	return lookup(r.AlarmCodes, value)
}

// ErrorCode is the dotted error code of a COMMAND_REPLY frame's error value.
func (r *Registry) ErrorCode(value uint16) (string, bool) {
	return lookup(r.ErrorCodes, value)
}

// StampCapabilities sets Capability.tiny_schema_id for standard commands an adapter left at 0.
func (r *Registry) StampCapabilities(set *capabilityv3.CapabilitySet) {
	for _, c := range set.GetCapabilities() {
		if c.GetTinySchemaId() != 0 {
			continue
		}
		if id, ok := r.CommandSchema(c.GetCommandId()); ok {
			c.TinySchemaId = uint32(id)
		}
	}
}

func lookup(codes []RegistryCode, value uint16) (string, bool) {
	for _, c := range codes {
		if c.Value == value {
			return c.Code, true
		}
	}
	return "", false
}
