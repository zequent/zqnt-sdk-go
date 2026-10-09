// Command example is a minimal drone adapter: two registered commands, telemetry over v3.
//
// Environment: BACKEND_ADDR (default localhost:50051), LISTEN_ADDR (default :9090),
// DEVICE_SN (default SN-DEMO-001).
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	edgesdk "github.com/Zequent/zqnt-edge-sdk-go/v2"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
)

var altitudeInput = map[string]any{
	"type":       "object",
	"properties": map[string]any{"altitude": map[string]any{"type": "number", "minimum": 2}},
}

// Drone registers what it can do; the SDK derives capabilities and dispatch from that.
type Drone struct {
	adapter.Base
	log    *slog.Logger
	events adapter.CommandEventPublisher
}

func newDrone(log *slog.Logger) *Drone {
	d := &Drone{log: log}
	d.SetAssetType("ASSET_TYPE_DRONE")
	d.MustRegisterCommand("flight.takeoff", altitudeInput, nil, d.takeOff,
		adapter.WithDescription("Take off and climb to the given altitude above the takeoff point"),
		adapter.WithCompletion(domains.CompletionAsynchronous, "flight.takeoff.completed"))
	d.MustRegisterCommand("flight.return_to_home", altitudeInput, nil, d.returnToHome)
	d.DeclareTelemetryField(domains.TelemetryField{Key: "drone.gear", Type: domains.TelemetryValueNumber})
	return d
}

func (d *Drone) takeOff(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	d.log.Info("take-off", "sn", req.SN, "params", req.Params)
	go func() {
		time.Sleep(3 * time.Second)
		if err := d.events.PublishCommandExecutionEvent(context.Background(), &domains.CommandExecutionEvent{
			SN: req.SN, CommandExecutionID: req.CommandExecutionID, ExternalExecutionID: req.TID,
			CommandID: req.CommandID, Status: domains.CommandExecutionSucceeded,
		}); err != nil {
			d.log.Error("completion event not delivered", "error", err)
		}
	}()
	return domains.Success("climbing", req.SN), nil
}

func (d *Drone) returnToHome(_ context.Context, req *domains.CustomCommandRequest) (*domains.CommandResult, error) {
	d.log.Info("return to home", "sn", req.SN)
	return domains.Success("returning", req.SN), nil
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	backendAddr := envOr("BACKEND_ADDR", "localhost:50051")
	listenAddr := envOr("LISTEN_ADDR", ":9090")
	deviceSN := envOr("DEVICE_SN", "SN-DEMO-001")

	drone := newDrone(log)
	client, err := edgesdk.NewEdgeClient(backendAddr, deviceSN, drone, edgesdk.WithLogger(log))
	if err != nil {
		log.Error("failed to create EdgeClient", "error", err)
		os.Exit(1)
	}
	drone.events = client.LiveData()

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Error("failed to listen", "addr", listenAddr, "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				battery := 80.0
				_ = client.LiveData().PublishTelemetrySample(ctx, &domains.TelemetrySample{
					SN: deviceSN, Position: &domains.GeoPoint{Lat: 52.52, Lon: 13.405}, BatteryPercent: &battery,
					Details: map[string]any{"drone.gear": 1},
				})
			}
		}
	}()

	go func() {
		<-ctx.Done()
		if shutdownErr := client.Shutdown(context.Background()); shutdownErr != nil {
			log.Error("shutdown error", "error", shutdownErr)
		}
	}()

	log.Info("edge-go-sdk example started", "sn", deviceSN, "backend", backendAddr, "listen", listenAddr)
	if serveErr := client.StartServing(ctx, lis); serveErr != nil {
		log.Error("server exited", "error", serveErr)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
