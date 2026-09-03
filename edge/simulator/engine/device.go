// Package engine is the simulator's kinematics/state-machine core: one Device per simulated
// aircraft, each running its own goroutine that owns that device's mutable flight state
// exclusively -- commands arrive over a channel and are applied there, interleaved with a fixed
// tick that advances the simulated flight and reports a telemetry Snapshot. No shared mutable
// state, no locks: the goroutine is the lock.
package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

type cmdKind int

const (
	cmdTakeOff cmdKind = iota
	cmdGoTo
	cmdReturnToHome
	cmdEnterManualControl
	cmdExitManualControl
	cmdManualInput
)

type command struct {
	kind    cmdKind
	payload any
	reply   chan *domains.CommandResult
}

type takeOffPayload struct {
	tid string
	alt float64
}

type goToPayload struct {
	tid  string
	dest Position
}

type rthPayload struct {
	tid string
}

// Device is a simulated aircraft. Its exported methods are safe to call from any goroutine (they
// hand a command to the device's own goroutine and wait for the reply); all simulation state lives
// inside Run's local `st`, touched only from there.
type Device struct {
	SN   string
	Home Position

	cmds chan command
	log  *slog.Logger
}

// NewDevice creates a Device. Call Run in its own goroutine to actually start simulating it.
func NewDevice(sn string, home Position, log *slog.Logger) *Device {
	return &Device{SN: sn, Home: home, cmds: make(chan command, 32), log: log}
}

// Run is the device's simulation loop: ticks at tickInterval (advancing kinematics and calling
// publish with the resulting Snapshot) and drains commands as they arrive. Blocks until ctx is
// cancelled -- call it in its own goroutine, one per device.
func (d *Device) Run(ctx context.Context, tickInterval time.Duration, publish func(Snapshot)) {
	st := newState(d.Home)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case cmd := <-d.cmds:
			var result *domains.CommandResult
			switch cmd.kind {
			case cmdTakeOff:
				p := cmd.payload.(takeOffPayload)
				result = st.takeOff(d.SN, p.tid, p.alt)
			case cmdGoTo:
				p := cmd.payload.(goToPayload)
				result = st.goTo(d.SN, p.tid, p.dest)
			case cmdReturnToHome:
				p := cmd.payload.(rthPayload)
				result = st.returnToHome(d.SN, p.tid)
			case cmdEnterManualControl:
				result = st.enterManualControl(d.SN)
			case cmdExitManualControl:
				result = st.exitManualControl(d.SN)
			case cmdManualInput:
				result = st.manualControlInput(d.SN, cmd.payload.(domains.ManualControlInput))
			default:
				result = domains.Error("unknown command", d.SN)
			}
			cmd.reply <- result

		case <-ticker.C:
			st.advance(tickInterval.Seconds())
			publish(st.snapshot(d.SN))
		}
	}
}

// send delivers a command to this device's own goroutine and waits for its result, respecting
// ctx cancellation on both the send and the reply.
func (d *Device) send(ctx context.Context, kind cmdKind, payload any) (*domains.CommandResult, error) {
	reply := make(chan *domains.CommandResult, 1)
	select {
	case d.cmds <- command{kind: kind, payload: payload, reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-reply:
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *Device) TakeOff(ctx context.Context, tid string, alt float64) (*domains.CommandResult, error) {
	return d.send(ctx, cmdTakeOff, takeOffPayload{tid: tid, alt: alt})
}

func (d *Device) GoTo(ctx context.Context, tid string, dest Position) (*domains.CommandResult, error) {
	return d.send(ctx, cmdGoTo, goToPayload{tid: tid, dest: dest})
}

func (d *Device) ReturnToHome(ctx context.Context, tid string) (*domains.CommandResult, error) {
	return d.send(ctx, cmdReturnToHome, rthPayload{tid: tid})
}

func (d *Device) EnterManualControl(ctx context.Context) (*domains.CommandResult, error) {
	return d.send(ctx, cmdEnterManualControl, nil)
}

func (d *Device) ExitManualControl(ctx context.Context) (*domains.CommandResult, error) {
	return d.send(ctx, cmdExitManualControl, nil)
}

func (d *Device) ManualInput(ctx context.Context, in domains.ManualControlInput) (*domains.CommandResult, error) {
	return d.send(ctx, cmdManualInput, in)
}
