package edgesdk

import (
	"context"
	"fmt"
	"net"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter"
	adaptergrpc "github.com/Zequent/zqnt-edge-sdk-go/adapter/grpc"
	"github.com/Zequent/zqnt-edge-sdk-go/connector"
	"github.com/Zequent/zqnt-edge-sdk-go/livedata"
	"github.com/Zequent/zqnt-edge-sdk-go/missionautonomy"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	connectorpb "github.com/zequent/zqnt-utils-golang/gen/connector/proto"
	livedatapb "github.com/zequent/zqnt-utils-golang/gen/livedata/proto"
	missionautonomypb "github.com/zequent/zqnt-utils-golang/gen/missionautonomy/proto"
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
//	// Produce telemetry
//	client.LiveData().ProduceTelemetryData(ctx, &domains.TelemetryRequestData{...})
type EdgeClient struct {
	cfg           *config
	adapterServer *adaptergrpc.Server
	grpcServer    *grpc.Server
	liveData      *livedata.ServiceImpl
	connectorSvc  *connector.ServiceImpl
	missionSvc    *missionautonomy.ServiceImpl
	backendConn   *grpc.ClientConn   // the main endpoint connection; always present, always closed on Shutdown
	extraConns    []*grpc.ClientConn // additional connections opened by WithConnectorAddr/WithLiveDataAddr/WithMissionAutonomyAddr, closed alongside backendConn
}

// NewEdgeClient creates and configures an EdgeClient.
//
// endpoint is the address of the backend gRPC services (e.g. "backend:50051").
// sn is the serial number of the asset this SDK instance manages.
// edgeAdapter is the integrator-provided hardware control implementation.
// opts are optional configuration overrides (see [Option] functions).
//
// The backend connection uses insecure credentials by default; wrap with
// grpc.WithTransportCredentials for TLS in production.
func NewEdgeClient(endpoint, sn string, edgeAdapter adapter.EdgeAdapter, opts ...Option) (*EdgeClient, error) {
	cfg := defaultConfig(endpoint, sn)
	for _, o := range opts {
		o(cfg)
	}

	// Dial the main endpoint. Whichever of connector/live-data/mission-autonomy wasn't given its
	// own address via WithConnectorAddr/WithLiveDataAddr/WithMissionAutonomyAddr uses this
	// connection -- correct only if something in front of endpoint multiplexes all three (see
	// config.go's doc comment on connectorAddr/liveDataAddr/missionAutonomyAddr).
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("edge-go-sdk: failed to connect to backend at %s: %w", endpoint, err)
	}

	log := cfg.logger
	var extraConns []*grpc.ClientConn

	dialOrShare := func(addr string) (*grpc.ClientConn, error) {
		if addr == "" {
			return conn, nil
		}
		c, dialErr := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	// Build outbound service clients.
	ldSvc := livedata.NewServiceImpl(livedatapb.NewLiveDataServiceClient(liveDataConn), log)
	connSvc := connector.NewServiceImpl(connectorpb.NewConnectorServiceClient(connectorConn), log)
	maSvc := missionautonomy.NewServiceImpl(missionautonomypb.NewMissionAutonomyServiceClient(missionConn), log)

	// Build the inbound gRPC server for EdgeAdapterService.
	grpcSrv := grpc.NewServer()
	adapterSrv := adaptergrpc.NewServer(edgeAdapter, log)
	adapterSrv.RegisterWith(grpcSrv)

	return &EdgeClient{
		cfg:           cfg,
		adapterServer: adapterSrv,
		grpcServer:    grpcSrv,
		liveData:      ldSvc,
		connectorSvc:  connSvc,
		missionSvc:    maSvc,
		backendConn:   conn,
		extraConns:    extraConns,
	}, nil
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
