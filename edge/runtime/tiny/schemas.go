package tiny

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

const (
	SchemaPosition      uint16 = 1
	SchemaAlarm         uint16 = 2
	SchemaStatus        uint16 = 3
	SchemaCommandReply  uint16 = 4
	SchemaCommandCancel uint16 = 5

	firstCommandSchema      uint16 = 0x0100
	lastCommandSchema       uint16 = 0x0FFF
	firstOrganizationSchema uint16 = 0x8000
)

const (
	MaxResultSize = MaxBodySize - 5
	MaxParamsSize = MaxBodySize - 6
)

const (
	StatusCharging uint8 = 1 << iota
	StatusExternalPower
	StatusGNSSFix
	StatusClockUnsynced
	StatusTamper
)

const (
	unknownU8       = 0xFF
	unknownI8       = math.MinInt8
	unknownAltitude = math.MinInt16
)

// IsCommandSchema reports whether a schema id names a downlink COMMAND frame layout.
func IsCommandSchema(schema uint16) bool {
	return schema >= firstCommandSchema && schema <= lastCommandSchema || schema >= firstOrganizationSchema
}

// Seconds is t as a frame time: whole seconds since TimeBase.
func Seconds(t time.Time) (uint32, error) {
	s := t.Unix() - TimeBase.Unix()
	if s < 0 || s > math.MaxUint32 {
		return 0, fmt.Errorf("tiny: time %s outside the frame time range", t.UTC().Format(time.RFC3339))
	}
	return uint32(s), nil
}

// Time is the instant of a frame time.
func Time(seconds uint32) time.Time {
	return TimeBase.Add(time.Duration(seconds) * time.Second)
}

// Position is a POSITION frame. Unknown values are NaN (Altitude, Speed, Heading) or -1 (Battery).
type Position struct {
	Time      time.Time
	Latitude  float64
	Longitude float64
	Altitude  float64
	Speed     float64
	Heading   float64
	Battery   int
}

// Alarm is an ALARM frame; Latitude and Longitude are NaN when the alarm has no position.
type Alarm struct {
	Time      time.Time
	Code      uint16
	Severity  uint8
	Latitude  float64
	Longitude float64
}

// Status is a STATUS frame. Unknown values are -1 (Battery), NaN (Voltage) or nil.
type Status struct {
	Time        time.Time
	Battery     int
	Voltage     float64
	Flags       uint8
	Temperature *int8
	RSSI        *int8
}

// CommandReply is a COMMAND_REPLY frame: the outcome of the command sent with DownlinkSeq.
type CommandReply struct {
	DownlinkSeq uint16
	State       uint8
	Error       uint16
	Result      []byte
}

// Command is a downlink COMMAND frame; Schema is the command's tiny schema id.
type Command struct {
	Schema      uint16
	DownlinkSeq uint16
	Expires     time.Time
	Params      []byte
}

// CommandCancel is a downlink COMMAND_CANCEL frame.
type CommandCancel struct {
	DownlinkSeq uint16
	Expires     time.Time
}

func (p Position) Body() ([]byte, error) {
	t, err := Seconds(p.Time)
	if err != nil {
		return nil, err
	}
	lat, lon, err := coordinates(p.Latitude, p.Longitude)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint32(make([]byte, 0, 17), t)
	b = binary.BigEndian.AppendUint32(b, uint32(lat))
	b = binary.BigEndian.AppendUint32(b, uint32(lon))
	alt := int16(unknownAltitude)
	if !math.IsNaN(p.Altitude) {
		alt = int16(clamp(math.Round(p.Altitude), math.MinInt16+1, math.MaxInt16))
	}
	b = binary.BigEndian.AppendUint16(b, uint16(alt))
	speed := uint8(unknownU8)
	if !math.IsNaN(p.Speed) {
		speed = uint8(clamp(math.Round(p.Speed*2), 0, 254))
	}
	heading := uint8(unknownU8)
	if !math.IsNaN(p.Heading) {
		heading = uint8(int(math.Round(math.Mod(math.Mod(p.Heading, 360)+360, 360)/2)) % 180)
	}
	return append(b, speed, heading, percent(p.Battery)), nil
}

func DecodePosition(body []byte) (Position, error) {
	if len(body) != 17 {
		return Position{}, sizeError(SchemaPosition, len(body))
	}
	p := Position{
		Time:      Time(binary.BigEndian.Uint32(body[0:4])),
		Latitude:  float64(int32(binary.BigEndian.Uint32(body[4:8]))) / 1e7,
		Longitude: float64(int32(binary.BigEndian.Uint32(body[8:12]))) / 1e7,
		Altitude:  math.NaN(),
		Speed:     math.NaN(),
		Heading:   math.NaN(),
		Battery:   fromPercent(body[16]),
	}
	if alt := int16(binary.BigEndian.Uint16(body[12:14])); alt != unknownAltitude {
		p.Altitude = float64(alt)
	}
	if body[14] != unknownU8 {
		p.Speed = float64(body[14]) / 2
	}
	if body[15] != unknownU8 {
		if body[15] >= 180 {
			return Position{}, fmt.Errorf("%w: heading %d", ErrMalformed, body[15])
		}
		p.Heading = float64(body[15]) * 2
	}
	return p, nil
}

func (a Alarm) Body() ([]byte, error) {
	t, err := Seconds(a.Time)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint32(make([]byte, 0, 15), t)
	b = binary.BigEndian.AppendUint16(b, a.Code)
	b = append(b, a.Severity)
	if math.IsNaN(a.Latitude) && math.IsNaN(a.Longitude) {
		return b, nil
	}
	lat, lon, err := coordinates(a.Latitude, a.Longitude)
	if err != nil {
		return nil, err
	}
	b = binary.BigEndian.AppendUint32(b, uint32(lat))
	return binary.BigEndian.AppendUint32(b, uint32(lon)), nil
}

func DecodeAlarm(body []byte) (Alarm, error) {
	if len(body) != 7 && len(body) != 15 {
		return Alarm{}, sizeError(SchemaAlarm, len(body))
	}
	a := Alarm{
		Time:      Time(binary.BigEndian.Uint32(body[0:4])),
		Code:      binary.BigEndian.Uint16(body[4:6]),
		Severity:  body[6],
		Latitude:  math.NaN(),
		Longitude: math.NaN(),
	}
	if len(body) == 15 {
		a.Latitude = float64(int32(binary.BigEndian.Uint32(body[7:11]))) / 1e7
		a.Longitude = float64(int32(binary.BigEndian.Uint32(body[11:15]))) / 1e7
	}
	return a, nil
}

func (s Status) Body() ([]byte, error) {
	t, err := Seconds(s.Time)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint32(make([]byte, 0, 9), t)
	voltage := uint8(unknownU8)
	if !math.IsNaN(s.Voltage) {
		voltage = uint8(clamp(math.Round(s.Voltage*10), 0, 254))
	}
	return append(b, percent(s.Battery), voltage, s.Flags, signed(s.Temperature), signed(s.RSSI)), nil
}

func DecodeStatus(body []byte) (Status, error) {
	if len(body) != 9 {
		return Status{}, sizeError(SchemaStatus, len(body))
	}
	s := Status{
		Time:        Time(binary.BigEndian.Uint32(body[0:4])),
		Battery:     fromPercent(body[4]),
		Voltage:     math.NaN(),
		Flags:       body[6],
		Temperature: fromSigned(body[7]),
		RSSI:        fromSigned(body[8]),
	}
	if body[5] != unknownU8 {
		s.Voltage = float64(body[5]) / 10
	}
	return s, nil
}

func (r CommandReply) Body() ([]byte, error) {
	if len(r.Result) > MaxResultSize {
		return nil, fmt.Errorf("%w: result of %d bytes, at most %d", ErrMalformed, len(r.Result), MaxResultSize)
	}
	b := binary.BigEndian.AppendUint16(make([]byte, 0, 5+len(r.Result)), r.DownlinkSeq)
	b = append(b, r.State)
	b = binary.BigEndian.AppendUint16(b, r.Error)
	return append(b, r.Result...), nil
}

func DecodeCommandReply(body []byte) (CommandReply, error) {
	if len(body) < 5 {
		return CommandReply{}, sizeError(SchemaCommandReply, len(body))
	}
	return CommandReply{
		DownlinkSeq: binary.BigEndian.Uint16(body[0:2]),
		State:       body[2],
		Error:       binary.BigEndian.Uint16(body[3:5]),
		Result:      append([]byte(nil), body[5:]...),
	}, nil
}

func (c Command) Body() ([]byte, error) {
	if !IsCommandSchema(c.Schema) {
		return nil, fmt.Errorf("%w: schema %d is not a command", ErrMalformed, c.Schema)
	}
	if len(c.Params) > MaxParamsSize {
		return nil, fmt.Errorf("%w: params of %d bytes, at most %d", ErrMalformed, len(c.Params), MaxParamsSize)
	}
	expires, err := Seconds(c.Expires)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint16(make([]byte, 0, 6+len(c.Params)), c.DownlinkSeq)
	b = binary.BigEndian.AppendUint32(b, expires)
	return append(b, c.Params...), nil
}

func DecodeCommand(schema uint16, body []byte) (Command, error) {
	if !IsCommandSchema(schema) {
		return Command{}, fmt.Errorf("%w: schema %d is not a command", ErrMalformed, schema)
	}
	if len(body) < 6 {
		return Command{}, sizeError(schema, len(body))
	}
	return Command{
		Schema:      schema,
		DownlinkSeq: binary.BigEndian.Uint16(body[0:2]),
		Expires:     Time(binary.BigEndian.Uint32(body[2:6])),
		Params:      append([]byte(nil), body[6:]...),
	}, nil
}

func (c CommandCancel) Body() ([]byte, error) {
	expires, err := Seconds(c.Expires)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint16(make([]byte, 0, 6), c.DownlinkSeq)
	return binary.BigEndian.AppendUint32(b, expires), nil
}

func DecodeCommandCancel(body []byte) (CommandCancel, error) {
	if len(body) != 6 {
		return CommandCancel{}, sizeError(SchemaCommandCancel, len(body))
	}
	return CommandCancel{
		DownlinkSeq: binary.BigEndian.Uint16(body[0:2]),
		Expires:     Time(binary.BigEndian.Uint32(body[2:6])),
	}, nil
}

func coordinates(lat, lon float64) (int32, int32, error) {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return 0, 0, fmt.Errorf("tiny: invalid position %v, %v", lat, lon)
	}
	return int32(math.Round(lat * 1e7)), int32(math.Round(lon * 1e7)), nil
}

func percent(v int) uint8 {
	if v < 0 {
		return unknownU8
	}
	return uint8(min(v, 100))
}

func fromPercent(b byte) int {
	if b == unknownU8 {
		return -1
	}
	return int(b)
}

func signed(v *int8) byte {
	if v == nil || *v == unknownI8 {
		return byte(0x80)
	}
	return byte(*v)
}

func fromSigned(b byte) *int8 {
	if b == 0x80 {
		return nil
	}
	v := int8(b)
	return &v
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

func sizeError(schema uint16, n int) error {
	return fmt.Errorf("%w: schema %d with a body of %d bytes", ErrMalformed, schema, n)
}
