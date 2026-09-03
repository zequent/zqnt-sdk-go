// Command simulator is the v1.3.0-compat simulator's process entry point: registers one
// hardcoded simulated drone with Connector, publishes its telemetry once a second, and serves
// EdgeAdapterService so remote-control can command it (TakeOff/GoTo/ReturnToHome/manual control).
//
// This is the staged build order's step 2 proof-of-concept -- one hardcoded device end-to-end,
// deliberately not the full fleet/scheduler/auth surface described in the project plan. Those are
// later stages built on the same engine/adapter packages this wires together.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	edgesdk "github.com/Zequent/zqnt-edge-sdk-go"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/discovery"
	commonpb "github.com/Zequent/zqnt-edge-sdk-go/gen/common/proto"
	"github.com/Zequent/zqnt-edge-sdk-go/simulator"
	"github.com/Zequent/zqnt-edge-sdk-go/simulator/engine"

	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	backendAddr := envOr("BACKEND_ADDR", "localhost:8010")     // connector's gRPC (multiplexed on its HTTP port)
	listenAddr := envOr("LISTEN_ADDR", ":9190")                // this process's own EdgeAdapterService port
	advertiseAddr := envOr("ADVERTISE_ADDR", "localhost:9190") // what remote-control dials -- must be reachable from wherever remote-control runs, not just from here
	redisAddr := envOr("REDIS_ADDR", "localhost:6379")
	sn := envOr("DEVICE_SN", "SIM-DRONE-001")

	vendor := commonpb.AssetVendor_ASSET_VENDOR_ZQNT.String()
	assetType := commonpb.AssetTypeEnum_ASSET_TYPE_AIRCRAFT.String()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	simAdapter := simulator.NewSimulatedAdapter(log)

	client, err := edgesdk.NewEdgeClient(backendAddr, sn, simAdapter, edgesdk.WithLogger(log))
	if err != nil {
		log.Error("failed to create EdgeClient", "error", err)
		os.Exit(1)
	}

	// Register the device with Connector so it shows up as a real Asset.
	name := "Simulated Drone 001"
	connString := advertiseAddr
	registered, err := client.Connector().RegisterAsset(ctx, &domains.AssetDTO{
		SN:                     &sn,
		Name:                   &name,
		Type:                   assetType,
		Vendor:                 vendor,
		Connection:             "TCP",
		SystemConnectionString: &connString,
	})
	if err != nil {
		log.Error("RegisterAsset failed", "sn", sn, "error", err)
		os.Exit(1)
	}
	if registered == nil {
		log.Error("RegisterAsset returned no asset -- check connector logs", "sn", sn)
		os.Exit(1)
	}
	log.Info("asset registered with Connector", "sn", sn, "id", registered.ID)

	// Make the device reachable: write the vendor->endpoint and sn->vendor Redis records
	// remote-control's GrpcEndpointRouter resolves on every command (see discovery package doc).
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()
	registrar := discovery.NewRegistrar(rdb)

	if err := registrar.RegisterEndpoint(ctx, vendor, assetType, advertiseAddr); err != nil {
		log.Error("RegisterEndpoint failed", "vendor", vendor, "error", err)
		os.Exit(1)
	}
	if err := registrar.RegisterAssetVendor(ctx, sn, vendor); err != nil {
		log.Error("RegisterAssetVendor failed", "sn", sn, "error", err)
		os.Exit(1)
	}
	log.Info("edge endpoint registered", "vendor", vendor, "endpoint", advertiseAddr)

	// Start simulating: one device, ticking at 1Hz, publishing telemetry each tick.
	home := engine.Position{Lat: 52.5200, Lon: 13.4050, Alt: 34} // Berlin-ish, arbitrary
	simAdapter.AddDevice(ctx, sn, home, time.Second, func(snap engine.Snapshot) {
		publishTelemetry(ctx, client, log, snap)
	})

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Error("failed to listen", "addr", listenAddr, "error", err)
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		log.Info("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := registrar.DeregisterEndpoint(shutdownCtx, vendor); err != nil {
			log.Error("DeregisterEndpoint failed", "vendor", vendor, "error", err)
		}
		if err := client.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown error", "error", err)
		}
	}()

	log.Info("simulator started", "sn", sn, "backend", backendAddr, "listen", listenAddr, "advertise", advertiseAddr)
	if err := client.StartServing(ctx, lis); err != nil {
		log.Error("server exited", "error", err)
	}
}

// publishTelemetry maps one engine.Snapshot to the AssetTelemetry shape and sends it. Dockless
// simulated drones publish TelemetryTypeAsset, not TelemetryTypeSubAsset -- mirrors
// adapters/mavlink-adapter's own AssetTelemetry publish for the same "no physical dock" reason
// (see engine/state.go's Mode.AssetMode doc comment).
func publishTelemetry(ctx context.Context, client *edgesdk.EdgeClient, log *slog.Logger, snap engine.Snapshot) {
	lat, lon, alt := float32(snap.Pos.Lat), float32(snap.Pos.Lon), float32(snap.Pos.Alt)
	heading := float32(snap.Heading)
	battery := float32(snap.BatteryPct)
	mode := snap.Mode.AssetMode()
	manual := snap.Manual
	now := time.Now()

	data := &domains.TelemetryRequestData{
		SN:        snap.SN,
		Timestamp: now,
		Type:      domains.TelemetryTypeAsset,
		AssetTelemetry: &domains.AssetTelemetryData{
			ID:                     snap.SN,
			Timestamp:              now,
			Latitude:               &lat,
			Longitude:              &lon,
			AbsoluteAltitude:       &alt,
			RelativeAltitude:       &alt,
			Heading:                &heading,
			Mode:                   &mode,
			SubAssetPercentage:     &battery, // reused as "this asset's own battery %" -- no separate sub-asset, per mavlink-adapter's own convention
			HasActiveManualControl: &manual,
		},
	}
	if err := client.LiveData().ProduceTelemetryData(ctx, data); err != nil {
		log.Warn("failed to publish telemetry", "sn", snap.SN, "error", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
