package edgesdk

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter"
	adaptergrpc "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/grpc"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/auth"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/connector"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/gateway"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/retry"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/livedata"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/missionautonomy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	connectorpb "github.com/zequent/zqnt-utils-golang/v2/gen/connector/proto"
	livedatapb "github.com/zequent/zqnt-utils-golang/v2/gen/livedata/proto"
	missionautonomypb "github.com/zequent/zqnt-utils-golang/v2/gen/missionautonomy/proto"
	remotecontrolpb "github.com/zequent/zqnt-utils-golang/v2/gen/remotecontrol/proto"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"
	telemetryv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/telemetry/v3"
)

// EdgeClient is the main entry point of the edge-go-sdk.
//
// It wires together:
//   - An [adapter.EdgeAdapter] implementation (provided by the integrator)
//   - A gRPC server that exposes the EdgeAdapterService over the network
//   - Client stubs for LiveData, Connector, and MissionAutonomy backend services
//
// Typical usage:
//
//	client, err := edgesdk.NewEdgeClient("grpc-backend:50051", "DEVICE-SN", myAdapter)
//	if err != nil { ... }
//
//	lis, _ := net.Listen("tcp", ":9090")
//	go client.StartServing(ctx, lis)
//
//	// Publish telemetry
//	client.LiveData().PublishTelemetrySample(ctx, &domains.TelemetrySample{...})
type EdgeClient struct {
	cfg           *config
	adapterServer *adaptergrpc.Server
	grpcServer    *grpc.Server
	liveData      *livedata.ServiceImpl
	connectorSvc  *connector.ServiceImpl
	missionSvc    *missionautonomy.ServiceImpl
	backendConn   *grpc.ClientConn   // the main endpoint connection; always present, always closed on Shutdown
	extraConns    []*grpc.ClientConn // additional connections opened by WithConnectorAddr/WithLiveDataAddr/WithMissionAutonomyAddr/WithRemoteControlAddr, closed alongside backendConn

	adapter      adapter.EdgeAdapter
	capabilities *gateway.CapabilityReporter
	tracked      sync.Map
	refresh      chan struct{}
	stopOnce     sync.Once
	stop         chan struct{}
}

// NewEdgeClient creates and configures an EdgeClient.
//
// endpoint is the address of the backend gRPC services (e.g. "backend:50051").
// sn is the serial number of the asset this SDK instance manages.
// edgeAdapter is the integrator-provided hardware control implementation.
// opts are optional configuration overrides (see [Option] functions).
//
// The backend connection is plaintext; every call carries the adapter's edge credential
// (ZQNT_EDGE_TOKEN / [WithEdgeToken]), and the adapter's own server only accepts commands signed
// by the platform (ZQNT_PLATFORM_PUBLIC_KEY / [WithPlatformPublicKey]). See package auth.
func NewEdgeClient(endpoint, sn string, edgeAdapter adapter.EdgeAdapter, opts ...Option) (*EdgeClient, error) {
	cfg := defaultConfig(endpoint, sn)
	for _, o := range opts {
		o(cfg)
	}

	// Dial the main endpoint. Whichever of connector/live-data/mission-autonomy wasn't given its
	// own address via WithConnectorAddr/WithLiveDataAddr/WithMissionAutonomyAddr uses this
	// connection -- correct only if something in front of endpoint multiplexes all three (see
	// config.go's doc comment on connectorAddr/liveDataAddr/missionAutonomyAddr).
	log := cfg.logger

	// Every call into the platform carries the adapter's edge credential; the platform refuses
	// calls without one (see package auth).
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if cfg.auth.EdgeToken != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(auth.BearerCredentials{Token: cfg.auth.EdgeToken}))
	} else {
		log.Warn("no ZQNT_EDGE_TOKEN: calls to the platform carry no credential and will be refused " +
			"(issue one in the console under Edge Credentials)")
	}

	// Only the platform may command the device: every inbound call must carry its service token.
	guard, err := auth.NewGuard(cfg.auth, log)
	if err != nil {
		return nil, fmt.Errorf("edge-go-sdk: %w", err)
	}

	conn, err := grpc.NewClient(endpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("edge-go-sdk: failed to connect to backend at %s: %w", endpoint, err)
	}

	var extraConns []*grpc.ClientConn

	dialOrShare := func(addr string) (*grpc.ClientConn, error) {
		if addr == "" {
			return conn, nil
		}
		c, dialErr := grpc.NewClient(addr, dialOpts...)
		if dialErr != nil {
			return nil, fmt.Errorf("edge-go-sdk: failed to connect to %s: %w", addr, dialErr)
		}
		extraConns = append(extraConns, c)
		return c, nil
	}

	connectorConn, err := dialOrShare(cfg.connectorAddr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	liveDataConn, err := dialOrShare(cfg.liveDataAddr)
	if err != nil {
		_ = conn.Close()
		for _, c := range extraConns {
			_ = c.Close()
		}
		return nil, err
	}
	missionConn, err := dialOrShare(cfg.missionAutonomyAddr)
	if err != nil {
		_ = conn.Close()
		for _, c := range extraConns {
			_ = c.Close()
		}
		return nil, err
	}
	remoteControlConn, err := dialOrShare(cfg.remoteControlAddr)
	if err != nil {
		_ = conn.Close()
		for _, c := range extraConns {
			_ = c.Close()
		}
		return nil, err
	}

	// Build outbound service clients. remote-control serves the v3 EdgeGatewayService, live-data
	// the v3 TelemetryIngestService; both fall back to v2 while the platform does not serve them.
	gatewayClient := edgev3.NewEdgeGatewayServiceClient(remoteControlConn)
	ldSvc := livedata.NewServiceImpl(livedatapb.NewLiveDataServiceClient(liveDataConn), log,
		livedata.WithGateway(gatewayClient),
		livedata.WithTelemetryIngest(telemetryv3.NewTelemetryIngestServiceClient(liveDataConn)))
	connSvc := connector.NewServiceImpl(connectorpb.NewConnectorServiceClient(connectorConn), log)
	maSvc := missionautonomy.NewServiceImpl(missionautonomypb.NewMissionAutonomyServiceClient(missionConn), log)

	// Build the inbound gRPC server for EdgeAdapterService.
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(guard.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(guard.StreamServerInterceptor()),
	)
	adapterSrv := adaptergrpc.NewServer(edgeAdapter, log)
	adapterSrv.RegisterWith(grpcSrv)
	// The v3 contract (ExecuteCommand only, no per-command RPCs) next to v2, same adapter.
	adaptergrpc.NewServerV3(edgeAdapter, log).RegisterWith(grpcSrv)

	client := &EdgeClient{
		cfg:           cfg,
		adapterServer: adapterSrv,
		grpcServer:    grpcSrv,
		liveData:      ldSvc,
		connectorSvc:  connSvc,
		missionSvc:    maSvc,
		backendConn:   conn,
		extraConns:    extraConns,
		adapter:       edgeAdapter,
		capabilities: gateway.NewCapabilityReporter(gatewayClient,
			remotecontrolpb.NewRemoteControlServiceClient(remoteControlConn), log),
		refresh: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	if ra, ok := edgeAdapter.(adapter.RegistryAdapter); ok {
		ra.CommandRegistry().OnChange(client.RefreshCapabilities)
	}
	return client, nil
}

// RefreshCapabilities reports the adapter's capabilities to the platform again. An adapter built
// on adapter.Base never needs it: every registry change triggers it. Repeated calls while a report
// is pending coalesce into one.
func (c *EdgeClient) RefreshCapabilities() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// TrackCapabilities adds an asset this adapter serves besides its own SN, e.g. one device of a
// fleet: its capabilities are reported now and on every refresh until UntrackCapabilities.
func (c *EdgeClient) TrackCapabilities(sn string) {
	c.tracked.Store(sn, struct{}{})
	c.RefreshCapabilities()
}

// UntrackCapabilities stops reporting an asset added with TrackCapabilities.
func (c *EdgeClient) UntrackCapabilities(sn string) {
	c.tracked.Delete(sn)
}

func (c *EdgeClient) reportedSNs() []string {
	sns := []string{c.cfg.sn}
	c.tracked.Range(func(key, _ any) bool {
		if sn := key.(string); sn != c.cfg.sn {
			sns = append(sns, sn)
		}
		return true
	})
	return sns
}

// reportCapabilities sends the capability snapshots on start and after every refresh, retrying a
// failed round with backoff until it gets through or a newer one replaces it.
func (c *EdgeClient) reportCapabilities(ctx context.Context) {
	attempt := 0
	var retryAfter <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-c.refresh:
			attempt = 0
		case <-retryAfter:
		}
		retryAfter = nil
		var failed error
		for _, sn := range c.reportedSNs() {
			if err := c.reportCapabilitiesOf(ctx, sn); err != nil {
				failed = err
				c.cfg.logger.Warn("capability report failed", "sn", sn, "error", err)
			}
		}
		if failed == nil {
			continue
		}
		attempt++
		delay := retry.ComputeDelay(attempt)
		c.cfg.logger.Warn("capability report round failed; retrying", "in", delay, "error", failed)
		retryAfter = time.After(delay)
	}
}

func (c *EdgeClient) reportCapabilitiesOf(ctx context.Context, sn string) error {
	caps, err := c.adapter.GetCapabilities(ctx, sn)
	if err != nil {
		return err
	}
	if caps.SN == "" {
		caps.SN = sn
	}
	revision, err := c.capabilities.Report(ctx, caps)
	if err != nil {
		return err
	}
	c.cfg.logger.Info("capabilities reported", "sn", caps.SN, "commands", len(caps.Capabilities), "revision", revision)
	return nil
}

// SN returns the configured serial number.
func (c *EdgeClient) SN() string { return c.cfg.sn }

// LiveData returns the live-data telemetry service.
func (c *EdgeClient) LiveData() *livedata.ServiceImpl { return c.liveData }

// Connector returns the connector CRUD service.
func (c *EdgeClient) Connector() *connector.ServiceImpl { return c.connectorSvc }

// MissionAutonomy returns the mission-autonomy service.
func (c *EdgeClient) MissionAutonomy() *missionautonomy.ServiceImpl { return c.missionSvc }

// StartServing begins accepting gRPC connections on lis.
// It blocks until the server is stopped or ctx is cancelled.
// Call [Shutdown] to gracefully stop.
func (c *EdgeClient) StartServing(ctx context.Context, lis net.Listener) error {
	c.cfg.logger.Info("EdgeClient gRPC server starting", "addr", lis.Addr())
	go c.reportCapabilities(ctx)
	c.RefreshCapabilities()

	errCh := make(chan error, 1)
	go func() { errCh <- c.grpcServer.Serve(lis) }()

	select {
	case <-ctx.Done():
		c.grpcServer.GracefulStop()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Shutdown gracefully stops the gRPC server, closes all telemetry streams,
// and closes the backend connection.
func (c *EdgeClient) Shutdown(ctx context.Context) error {
	c.cfg.logger.Info("EdgeClient shutting down")
	c.stopOnce.Do(func() { close(c.stop) })

	c.grpcServer.GracefulStop()

	if err := c.liveData.Shutdown(ctx); err != nil {
		c.cfg.logger.Error("error shutting down live-data service", "error", err)
	}

	err := c.backendConn.Close()
	for _, extra := range c.extraConns {
		if closeErr := extra.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}
