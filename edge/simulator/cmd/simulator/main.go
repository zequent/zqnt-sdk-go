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
	"errors"
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

	// connector/live-data/mission-autonomy are three independent services on three independent
	// ports in every real deployment topology (core/docker-compose.local.yml's *_SERVICE_HOST/
	// *_SERVICE_PORT, every existing adapter's application.properties) -- there is no single
	// unified "backend" address, despite NewEdgeClient's single positional endpoint arg. Passed
	// as the connector address below; live-data and mission-autonomy are overridden separately.
	connectorAddr := envOr("CONNECTOR_ADDR", "localhost:8010")
	liveDataAddr := envOr("LIVE_DATA_ADDR", "localhost:8003")
	missionAutonomyAddr := envOr("MISSION_AUTONOMY_ADDR", "localhost:8004")
	listenAddr := envOr("LISTEN_ADDR", ":9190")                // this process's own EdgeAdapterService port
	advertiseAddr := envOr("ADVERTISE_ADDR", "localhost:9190") // what remote-control dials -- must be reachable from wherever remote-control runs, not just from here
	redisAddr := envOr("REDIS_ADDR", "localhost:6379")
	sn := envOr("DEVICE_SN", "SIM-DRONE-001")

	vendor := commonpb.AssetVendor_ASSET_VENDOR_ZQNT.String()
	assetType := commonpb.AssetTypeEnum_ASSET_TYPE_AIRCRAFT.String()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	simAdapter := simulator.NewSimulatedAdapter(log)

	client, err := edgesdk.NewEdgeClient(connectorAddr, sn, simAdapter,
		edgesdk.WithLogger(log),
		edgesdk.WithLiveDataAddr(liveDataAddr),
		edgesdk.WithMissionAutonomyAddr(missionAutonomyAddr),
	)
	if err != nil {
		log.Error("failed to create EdgeClient", "error", err)
		os.Exit(1)
	}

	// Register the device with Connector so it shows up as a real Asset. RegisterAsset isn't an
	// upsert -- a restart against an sn that's already registered (the common case: this is a
	// long-running process, not a one-shot script) fails with "Asset already exists" rather than
	// returning the existing asset, so fall back to looking it up.
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
	if err != nil || registered == nil {
		log.Warn("RegisterAsset failed, checking whether it already exists", "sn", sn, "error", err)
		registered, err = client.Connector().GetAssetBySN(ctx, sn)
		if err != nil || registered == nil {
			log.Error("RegisterAsset failed and no existing asset found", "sn", sn, "error", err)
			os.Exit(1)
		}
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

	// StartServing's own ctx.Done() case (client.go) also fires GracefulStop() and returns the
	// moment ctx is cancelled -- running deregistration in a separate goroutine racing that same
	// signal let the process exit before the Redis write landed (caught live-verifying: the
	// endpoint stayed "online":true in Redis after a clean shutdown). Serve in the background
	// instead and run every shutdown step here, in order, before main returns.
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- client.StartServing(ctx, lis) }()

	log.Info("simulator started", "sn", sn, "connector", connectorAddr, "liveData", liveDataAddr, "missionAutonomy", missionAutonomyAddr, "listen", listenAddr, "advertise", advertiseAddr)

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
	if err := <-serveErrCh; err != nil && !errors.Is(err, context.Canceled) {
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
