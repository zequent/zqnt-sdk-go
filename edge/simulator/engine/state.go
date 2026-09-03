package engine

import (
	"math"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// Mode is the simulated device's coarse flight state.
type Mode int

const (
	ModeDocked Mode = iota
	ModeFlying
	ModeReturning
)

// AssetMode maps the simulator's own flight state to the platform's AssetMode enum name
// (asset.proto), the shape AssetTelemetryData.Mode expects. AssetMode has no FLYING/RETURNING
// state of its own (IDLE/DEBUGGING/WORKING/UPGRADING/...) -- WORKING is the established stand-in
// for "actively doing something" per adapters/mavlink-adapter's own _map_flight_mode, which this
// mirrors (dockless drones publish AssetTelemetry, not SubAssetTelemetry -- see simulator/README).
func (m Mode) AssetMode() string {
	if m == ModeFlying || m == ModeReturning {
		return "ASSET_MODE_WORKING"
	}
	return "ASSET_MODE_IDLE"
}

const (
	defaultHorizontalSpeedMS = 8.0  // m/s cruise speed -- typical small-drone pace
	defaultClimbRateMS       = 2.0  // m/s vertical
	batteryDrainPctPerS      = 0.03 // while flying/returning
	batteryChargePctPerS     = 0.5  // while docked and not full
	manualYawRateDegPerS     = 60.0 // deg/s at full yaw deflection
)

// Snapshot is one tick's read-only telemetry sample.
type Snapshot struct {
	SN         string
	Mode       Mode
	Pos        Position
	Heading    float64
	BatteryPct float64
	Manual     bool
}

// state holds one device's mutable flight state. It is owned exclusively by that device's own
// goroutine (see Device.Run) -- apply and advance are never called concurrently, so no locking.
type state struct {
	mode        Mode
	pos         Position
	home        Position
	target      *Position
	heading     float64
	batteryPct  float64
	manual      bool
	manualInput domains.ManualControlInput
}

func newState(home Position) *state {
	return &state{mode: ModeDocked, pos: home, home: home, batteryPct: 100}
}

func (s *state) snapshot(sn string) Snapshot {
	return Snapshot{SN: sn, Mode: s.mode, Pos: s.pos, Heading: s.heading, BatteryPct: s.batteryPct, Manual: s.manual}
}

// ---- command application ------------------------------------------------------

func (s *state) takeOff(sn, tid string, alt float64) *domains.CommandResult {
	if s.mode != ModeDocked {
		return domains.ErrorWithTID("takeOff rejected: already airborne", tid, sn)
	}
	if alt <= 0 {
		alt = 30
	}
	s.mode = ModeFlying
	target := Position{Lat: s.pos.Lat, Lon: s.pos.Lon, Alt: alt}
	s.target = &target
	return domains.SuccessWithTID("takeOff accepted", tid, sn)
}

func (s *state) goTo(sn, tid string, dest Position) *domains.CommandResult {
	if s.mode != ModeFlying {
		return domains.ErrorWithTID("goTo rejected: device is not airborne", tid, sn)
	}
	if dest.Alt <= 0 {
		dest.Alt = s.pos.Alt // hold current altitude if the request didn't specify one
	}
	s.target = &dest
	return domains.SuccessWithTID("goTo accepted", tid, sn)
}

func (s *state) returnToHome(sn, tid string) *domains.CommandResult {
	if s.mode == ModeDocked {
		return domains.SuccessWithTID("already home", tid, sn)
	}
	s.mode = ModeReturning
	target := s.home
	s.target = &target
	return domains.SuccessWithTID("returnToHome accepted", tid, sn)
}

func (s *state) enterManualControl(sn string) *domains.CommandResult {
	s.manual = true
	return domains.Success("manual control session opened", sn)
}

func (s *state) exitManualControl(sn string) *domains.CommandResult {
	s.manual = false
	s.manualInput = domains.ManualControlInput{}
	return domains.Success("manual control session closed", sn)
}

func (s *state) manualControlInput(sn string, in domains.ManualControlInput) *domains.CommandResult {
	if !s.manual {
		return domains.NotImplemented("no active manual control session", sn)
	}
	if s.mode == ModeDocked {
		s.mode = ModeFlying // joystick input from the ground implicitly launches, same as a real RC stick push
	}
	s.manualInput = in
	s.target = nil // manual input overrides any autonomous target until the session ends
	return domains.Success("manual input applied", sn)
}

// ---- tick advance --------------------------------------------------------------

// advance moves the simulation forward by dtSeconds of elapsed time.
func (s *state) advance(dtSeconds float64) {
	switch s.mode {
	case ModeFlying, ModeReturning:
		if s.manual {
			s.advanceManual(dtSeconds)
		} else if s.target != nil {
			s.advanceTowardTarget(dtSeconds)
		}
		s.batteryPct = clamp(s.batteryPct-batteryDrainPctPerS*dtSeconds, 0, 100)
	case ModeDocked:
		s.batteryPct = clamp(s.batteryPct+batteryChargePctPerS*dtSeconds, 0, 100)
	}
}

func (s *state) advanceTowardTarget(dtSeconds float64) {
	maxHoriz := defaultHorizontalSpeedMS * dtSeconds
	maxVert := defaultClimbRateMS * dtSeconds
	next, arrived := moveToward(s.pos, *s.target, maxHoriz, maxVert)
	if next != s.pos {
		s.heading = headingTo(s.pos, next)
	}
	s.pos = next
	if !arrived {
		return
	}
	if s.mode == ModeReturning {
		s.pos = s.home
		s.mode = ModeDocked
	}
	s.target = nil // hover in place once a GoTo/TakeOff target is reached
}

// advanceManual applies one tick of joystick input (Pitch = forward/back along current heading,
// Yaw = turn rate, Throttle = climb/descend). Roll (strafe) isn't modeled -- out of scope for the
// POC's kinematics, same as every other purely-cosmetic axis.
func (s *state) advanceManual(dtSeconds float64) {
	in := s.manualInput
	if in.Yaw != nil {
		s.heading = math.Mod(s.heading+float64(*in.Yaw)*manualYawRateDegPerS*dtSeconds+360, 360)
	}
	if in.Pitch != nil {
		speed := float64(*in.Pitch) * defaultHorizontalSpeedMS
		rad := s.heading * math.Pi / 180
		s.pos.Lat += (speed * dtSeconds * math.Cos(rad)) / metersPerDegLat
		s.pos.Lon += (speed * dtSeconds * math.Sin(rad)) / metersPerDegLon(s.pos.Lat)
	}
	if in.Throttle != nil {
		s.pos.Alt = math.Max(0, s.pos.Alt+float64(*in.Throttle)*defaultClimbRateMS*dtSeconds)
	}
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
