// Command example demonstrates how to use the edge-go-sdk to create a
// minimal drone adapter that handles TakeOff and returns NOT_IMPLEMENTED
// for all other commands.
//
// Before running:
//  1. Run `make proto` to generate the proto Go bindings.
//  2. Run `go mod tidy` to download dependencies.
//  3. Set the BACKEND_ADDR and LISTEN_ADDR environment variables, or use
//     the defaults below.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	edgesdk "github.com/Zequent/zqnt-edge-sdk-go"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// MyDroneAdapter is a minimal EdgeAdapter implementation.
// Embed UnimplementedEdgeAdapter for all the NOT_IMPLEMENTED defaults, then
// override only the commands this specific hardware supports.
type MyDroneAdapter struct {
	adapter.UnimplementedEdgeAdapter
	log *slog.Logger
}

func (a *MyDroneAdapter) TakeOff(ctx context.Context, req *domains.TakeOffRequest) (*domains.CommandResult, error) {
	a.log.Info("TakeOff requested", "sn", req.SN, "alt", req.Coordinates.Alt)
	// TODO: send takeoff command to real hardware here.
	return domains.SuccessWithTID("takeOff accepted", req.TID, req.SN), nil
}

func (a *MyDroneAdapter) ReturnToHome(ctx context.Context, req *domains.ReturnToHomeRequest) (*domains.CommandResult, error) {
	a.log.Info("ReturnToHome requested", "sn", req.SN)
	return domains.SuccessWithTID("returnToHome accepted", req.TID, req.SN), nil
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	backendAddr := envOr("BACKEND_ADDR", "localhost:50051")
	listenAddr := envOr("LISTEN_ADDR", ":9090")
	deviceSN := envOr("DEVICE_SN", "SN-DEMO-001")

	myAdapter := &MyDroneAdapter{log: log}

	client, err := edgesdk.NewEdgeClient(
		backendAddr,
		deviceSN,
		myAdapter,
		edgesdk.WithLogger(log),
	)
	if err != nil {
		log.Error("failed to create EdgeClient", "error", err)
		os.Exit(1)
	}

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Error("failed to listen", "addr", listenAddr, "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("edge-go-sdk example started", "sn", deviceSN, "backend", backendAddr, "listen", listenAddr)

	// StartServing blocks; Shutdown is called when ctx is cancelled.
	go func() {
		<-ctx.Done()
		log.Info("shutting down...")
		if shutdownErr := client.Shutdown(context.Background()); shutdownErr != nil {
			log.Error("shutdown error", "error", shutdownErr)
		}
	}()

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
