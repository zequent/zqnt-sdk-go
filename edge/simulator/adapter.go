// Package simulator implements adapter.EdgeAdapter against a fleet of purely-simulated devices --
// no hardware, no firmware. See engine/ for the per-device kinematics/state-machine core this
// wraps, and cmd/simulator for the process entry point.
package simulator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go-simulator/engine"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// SimulatedAdapter implements adapter.EdgeAdapter for many SN-keyed simulated devices behind one
// gRPC endpoint -- the platform's "one adapter process, many devices" model (see
// discovery.go's package doc: routing is per-vendor, not per-device, so this is the shape the
// wire contract actually expects, not a simulator-specific simplification). Every EdgeAdapter
// method here does the same thing: look the device up by the SN threaded through the request, and
// forward the command to that device's own goroutine (engine.Device).
//
// Commands this POC doesn't cover yet (LookAt, cover/charging, camera, etc.) fall through to
// UnimplementedEdgeAdapter's NOT_IMPLEMENTED defaults -- filling out the rest of the command
// surface is explicitly a later stage (see the project plan's "staged build order").
type SimulatedAdapter struct {
	adapter.UnimplementedEdgeAdapter

	mu      sync.RWMutex
	devices map[string]*engine.Device
	log     *slog.Logger
}

// NewSimulatedAdapter creates an empty fleet. Add devices with AddDevice.
func NewSimulatedAdapter(log *slog.Logger) *SimulatedAdapter {
	return &SimulatedAdapter{devices: make(map[string]*engine.Device), log: log}
}

// AddDevice registers a new simulated device and starts its goroutine ticking at tickInterval.
// publish is invoked from the device's own goroutine once per tick with its current telemetry
// snapshot -- typically wired to LiveDataService.ProduceTelemetryData. The device stops when ctx
// is cancelled.
func (a *SimulatedAdapter) AddDevice(ctx context.Context, sn string, home engine.Position, tickInterval time.Duration, publish func(engine.Snapshot)) *engine.Device {
	d := engine.NewDevice(sn, home, a.log)
	a.mu.Lock()
	a.devices[sn] = d
	a.mu.Unlock()
	go d.Run(ctx, tickInterval, publish)
	a.log.Info("simulated device added", "sn", sn, "home", home)
	return d
}

func (a *SimulatedAdapter) device(sn string) (*engine.Device, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	d, ok := a.devices[sn]
	if !ok {
		return nil, fmt.Errorf("simulator: no such device %q", sn)
	}
	return d, nil
}

func (a *SimulatedAdapter) TakeOff(ctx context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
	d, err := a.device(req.SN)
	if err != nil {
		return domains.ErrorWithTID(err.Error(), req.TID, req.SN), nil
	}
	return d.TakeOff(ctx, req.TID, req.Coordinates.Alt)
}

func (a *SimulatedAdapter) GoTo(ctx context.Context, req *domains.GoToRequest) (*domains.CommandResult, error) {
	d, err := a.device(req.SN)
	if err != nil {
		return domains.ErrorWithTID(err.Error(), req.TID, req.SN), nil
	}
	dest := engine.Position{Lat: req.Coordinates.Lat, Lon: req.Coordinates.Lon, Alt: req.Coordinates.Alt}
	return d.GoTo(ctx, req.TID, dest)
}

func (a *SimulatedAdapter) ReturnToHome(ctx context.Context, req *domains.ReturnToHomeRequest) (*domains.CommandResult, error) {
	d, err := a.device(req.SN)
	if err != nil {
		return domains.ErrorWithTID(err.Error(), req.TID, req.SN), nil
	}
	return d.ReturnToHome(ctx, req.TID)
}

func (a *SimulatedAdapter) EnterManualControl(ctx context.Context, sn string) (*domains.CommandResult, error) {
	d, err := a.device(sn)
	if err != nil {
		return domains.Error(err.Error(), sn), nil
	}
	return d.EnterManualControl(ctx)
}

func (a *SimulatedAdapter) ExitManualControl(ctx context.Context, sn string) (*domains.CommandResult, error) {
	d, err := a.device(sn)
	if err != nil {
		return domains.Error(err.Error(), sn), nil
	}
	return d.ExitManualControl(ctx)
}

func (a *SimulatedAdapter) ManualControlInput(ctx context.Context, in *domains.ManualControlInput) (*domains.CommandResult, error) {
	d, err := a.device(in.SN)
	if err != nil {
		return domains.Error(err.Error(), in.SN), nil
	}
	return d.ManualInput(ctx, *in)
}
