package tiny

import (
	_ "embed"
	"encoding/binary"
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
	Version    int               `json:"version"`
	Schemas    []RegistrySchema  `json:"schemas"`
	Commands   []RegistryCommand `json:"commands"`
	AlarmCodes []RegistryCode    `json:"alarm_codes"`
	ErrorCodes []RegistryCode    `json:"error_codes"`
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

// RegistryCommand is a standard command with the fixed layout of its COMMAND frame params.
type RegistryCommand struct {
	ID        uint16          `json:"id"`
	CommandID string          `json:"command_id"`
	Params    []RegistryParam `json:"params"`
}

// RegistryParam is one fixed-width field of a command's params; Scale is the unit of one integer
// step (0 = the integer itself).
type RegistryParam struct {
	Name  string  `json:"name"`
	Type  string  `json:"type"`
	Scale float64 `json:"scale"`
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

// Command is the standard command behind a COMMAND frame's schema id.
func (r *Registry) Command(schema uint16) (RegistryCommand, bool) {
	for _, c := range r.Commands {
		if c.ID == schema {
			return c, true
		}
	}
	return RegistryCommand{}, false
}

// DecodeParams reads the params of a standard command's COMMAND frame by their registry layout.
func (r *Registry) DecodeParams(schema uint16, params []byte) (map[string]float64, error) {
	c, ok := r.Command(schema)
	if !ok {
		return nil, fmt.Errorf("%w: schema %d is not a standard command", ErrMalformed, schema)
	}
	size := 0
	for _, p := range c.Params {
		size += p.width()
	}
	if len(params) != size {
		return nil, fmt.Errorf("%w: %s params of %d bytes, want %d", ErrMalformed, c.CommandID, len(params), size)
	}
	values := make(map[string]float64, len(c.Params))
	for _, p := range c.Params {
		var raw int64
		switch p.Type {
		case "i8":
			raw = int64(int8(params[0]))
		case "u8":
			raw = int64(params[0])
		case "i16":
			raw = int64(int16(binary.BigEndian.Uint16(params)))
		case "u16":
			raw = int64(binary.BigEndian.Uint16(params))
		case "i32":
			raw = int64(int32(binary.BigEndian.Uint32(params)))
		case "u32":
			raw = int64(binary.BigEndian.Uint32(params))
		}
		params = params[p.width():]
		if p.Scale != 0 {
			values[p.Name] = float64(raw) * p.Scale
		} else {
			values[p.Name] = float64(raw)
		}
	}
	return values, nil
}

func (p RegistryParam) width() int {
	switch p.Type {
	case "i8", "u8":
		return 1
	case "i16", "u16":
		return 2
	case "i32", "u32":
		return 4
	}
	panic(fmt.Sprintf("tiny: registry param type %q", p.Type))
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
