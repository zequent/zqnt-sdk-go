// Package local is the Edge Node's local API: a Unix socket on the node where Device Adapters
// attach with the v3 contracts they already speak to the platform.
//
// Adapters call the node socket instead of the platform:
//
//   - zqnt.telemetry.v3.TelemetryIngestService: samples (TELEMETRY lane), detections (BULK) and
//     alerts (ALARM from WARNING up, BULK below) go into the node's store and reach the platform
//     when a link is up.
//   - zqnt.edge.v3.EdgeGatewayService.ReportCapabilities: the capabilities go to the platform in
//     the STATUS lane (latest per asset), and the asset is bound to the reporting adapter.
//   - zqnt.edge.v3.EdgeGatewayService.PublishCommandEvent: events of commands the node runs on the
//     adapter complete those commands; other events go to the platform in the COMMAND_REPLY lane.
//   - NodeQueueService (QueueServiceName): the node's queue and a throttle per lane.
//
// Commands the platform delivers to the node run through the Server's command Handler: it calls
// zqnt.edge.v3.EdgeAdapterService.ExecuteCommand on the adapter that registered the command's
// asset, over that adapter's own Unix socket. An adapter names its socket in the
// AdapterEndpointHeader metadata of ReportCapabilities; a node with one adapter may configure it
// instead (Config.DefaultAdapterEndpoint).
//
// Nothing here listens on the network: the socket's permissions are the access control (see
// Listen), and adapters are only ever dialled on Unix sockets.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	edgev3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edge/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/command"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// AdapterEndpointHeader is the gRPC metadata key on ReportCapabilities that names the reporting
// adapter's own socket, e.g. "unix:///run/zqnt/adapters/dji.sock".
const AdapterEndpointHeader = "zqnt-adapter-endpoint"

// Publisher queues an envelope for the platform: the node session's Publish.
type Publisher interface {
	Publish(ctx context.Context, env *edgelinkv3.Envelope) (*edgelinkv3.MessageId, error)
}

// Config configures a Server.
type Config struct {
	Store     *store.Store
	Publisher Publisher
	// Connected reports whether the node has a session to the platform; nil reports false.
	Connected func() bool
	// DefaultAdapterEndpoint receives the commands for assets whose adapter did not name its
	// socket: the one adapter of a node, e.g. "unix:///run/zqnt/adapter.sock".
	DefaultAdapterEndpoint string
	// AdapterCredentials authenticate the node's calls into adapters; nil sends none.
	AdapterCredentials credentials.PerRPCCredentials
	// CancelTimeout bounds a CancelCommand call into an adapter (5 s).
	CancelTimeout time.Duration
	// WatchInterval is how often WatchQueueStats looks at the queue (1 s).
	WatchInterval time.Duration
	// ServerOptions are added to the gRPC server's, e.g. interceptors.
	ServerOptions []grpc.ServerOption
}

// Server serves the node socket. It is safe for concurrent use.
type Server struct {
	cfg    Config
	router *router
	grpc   *grpc.Server

	stopOnce sync.Once
	stopped  chan struct{}
}

// New checks cfg and builds the server; Serve starts it.
func New(cfg Config) (*Server, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("local: store is required")
	case cfg.Publisher == nil:
		return nil, errors.New("local: publisher is required")
	}
	if cfg.DefaultAdapterEndpoint != "" {
		if _, err := adapterTarget(cfg.DefaultAdapterEndpoint); err != nil {
			return nil, fmt.Errorf("local: %w", err)
		}
	}
	if cfg.Connected == nil {
		cfg.Connected = func() bool { return false }
	}
	if cfg.CancelTimeout <= 0 {
		cfg.CancelTimeout = 5 * time.Second
	}
	if cfg.WatchInterval <= 0 {
		cfg.WatchInterval = time.Second
	}
	var creds grpc.DialOption
	if cfg.AdapterCredentials != nil {
		creds = grpc.WithPerRPCCredentials(cfg.AdapterCredentials)
	}
	s := &Server{cfg: cfg, router: newRouter(creds, cfg.CancelTimeout), stopped: make(chan struct{})}
	s.grpc = grpc.NewServer(cfg.ServerOptions...)
	telemetryv3.RegisterTelemetryIngestServiceServer(s.grpc, &ingest{s: s})
	edgev3.RegisterEdgeGatewayServiceServer(s.grpc, &gateway{s: s})
	s.grpc.RegisterService(&queueServiceDesc, &queue{s: s})
	return s, nil
}

// Handler runs the platform's commands on the adapters: the command Runner's Handler.
func (s *Server) Handler() command.Handler { return s.router }

// Serve accepts adapters on lis (see Listen) until Stop.
func (s *Server) Serve(lis net.Listener) error { return s.grpc.Serve(lis) }

// Stop closes every adapter stream and connection.
func (s *Server) Stop() error {
	var err error
	s.stopOnce.Do(func() {
		close(s.stopped)
		s.grpc.Stop()
		err = s.router.close()
	})
	return err
}

// Assets maps every asset an adapter registered to that adapter's socket.
func (s *Server) Assets() map[string]string { return s.router.Assets() }

// QueueStats is the node's queue with a throttle per lane.
func (s *Server) QueueStats() (QueueStats, error) {
	return queueStats(s.cfg.Store, s.cfg.Connected())
}

func (s *Server) publish(ctx context.Context, env *edgelinkv3.Envelope, at *timestamppb.Timestamp) error {
	if at.IsValid() && at.AsTime().Unix() > 0 {
		env.DeviceTime = at
	}
	if _, err := s.cfg.Publisher.Publish(ctx, env); err != nil {
		return status.Errorf(codes.Unavailable, "the node could not queue it: %v", err)
	}
	return nil
}

type ingest struct {
	telemetryv3.UnimplementedTelemetryIngestServiceServer
	s *Server
}

func (g *ingest) PublishTelemetry(stream grpc.ClientStreamingServer[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse]) error {
	var resp telemetryv3.PublishTelemetryResponse
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&resp)
		}
		if err != nil {
			return err
		}
		sample := req.GetSample()
		if sample.GetAsset().GetSn() == "" {
			resp.Rejected++
			continue
		}
		env := &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Sample{Sample: sample}}
		if err := g.s.publish(stream.Context(), env, sample.GetObservedAt()); err != nil {
			return err
		}
		resp.Accepted++
	}
}

func (g *ingest) PublishDetections(stream grpc.ClientStreamingServer[telemetryv3.PublishDetectionsRequest, telemetryv3.PublishDetectionsResponse]) error {
	var resp telemetryv3.PublishDetectionsResponse
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&resp)
		}
		if err != nil {
			return err
		}
		batch := req.GetBatch()
		if batch.GetAsset().GetSn() == "" {
			resp.Rejected++
			continue
		}
		env := &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Detections{Detections: batch}}
		if err := g.s.publish(stream.Context(), env, batch.GetObservedAt()); err != nil {
			return err
		}
		resp.Accepted++
	}
}

func (g *ingest) PublishAlerts(stream grpc.ClientStreamingServer[telemetryv3.PublishAlertsRequest, telemetryv3.PublishAlertsResponse]) error {
	var resp telemetryv3.PublishAlertsResponse
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&resp)
		}
		if err != nil {
			return err
		}
		alert := req.GetAlert()
		if alert.GetAsset().GetSn() == "" || alert.GetCode() == "" {
			resp.Rejected++
			continue
		}
		env := &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Alert{Alert: alert}}
		if err := g.s.publish(stream.Context(), env, alert.GetOccurredAt()); err != nil {
			return err
		}
		resp.Accepted++
	}
}

type gateway struct {
	edgev3.UnimplementedEdgeGatewayServiceServer
	s *Server
}

func (g *gateway) ReportCapabilities(ctx context.Context, req *edgev3.ReportCapabilitiesRequest) (*edgev3.ReportCapabilitiesResponse, error) {
	caps := req.GetCapabilities()
	sn := caps.GetAssetSn()
	if sn == "" {
		return nil, status.Error(codes.InvalidArgument, "capabilities.asset_sn is required")
	}
	endpoint := g.s.cfg.DefaultAdapterEndpoint
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(AdapterEndpointHeader); len(v) > 0 && v[0] != "" {
			endpoint = v[0]
		}
	}
	if endpoint == "" {
		return nil, status.Errorf(codes.InvalidArgument, "name the adapter's socket in the %s header; the node has no default adapter", AdapterEndpointHeader)
	}
	if err := g.s.router.register(sn, endpoint); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	env := &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_Capabilities{Capabilities: caps}}
	if err := g.s.publish(ctx, env, caps.GetObservedAt()); err != nil {
		return nil, err
	}
	return &edgev3.ReportCapabilitiesResponse{AcceptedRevision: caps.GetRevision()}, nil
}

func (g *gateway) PublishCommandEvent(ctx context.Context, req *edgev3.PublishCommandEventRequest) (*edgev3.PublishCommandEventResponse, error) {
	ev := req.GetEvent()
	if ev.GetCommandExecutionId() == "" || ev.GetAsset().GetSn() == "" {
		return nil, status.Error(codes.InvalidArgument, "event.command_execution_id and event.asset.sn are required")
	}
	if ev.GetState() == capabilityv3.CommandState_COMMAND_STATE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "event.state is required")
	}
	if g.s.router.deliver(ev) {
		return &edgev3.PublishCommandEventResponse{}, nil
	}
	env := &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_CommandEvent{CommandEvent: ev}}
	if err := g.s.publish(ctx, env, ev.GetOccurredAt()); err != nil {
		return nil, err
	}
	return &edgev3.PublishCommandEventResponse{}, nil
}

type queue struct{ s *Server }

func (q *queue) getQueueStats(context.Context) (*structpb.Struct, error) {
	stats, err := q.s.QueueStats()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return stats.Struct(), nil
}

func (q *queue) watchQueueStats(stream grpc.ServerStream) error {
	tick := time.NewTicker(q.s.cfg.WatchInterval)
	defer tick.Stop()
	var last QueueStats
	var sentAt time.Time
	for {
		stats, err := q.s.QueueStats()
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		if sentAt.IsZero() || changed(last, stats) || time.Since(sentAt) >= WatchHeartbeat {
			if err := stream.SendMsg(stats.Struct()); err != nil {
				return err
			}
			last, sentAt = stats, time.Now()
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-q.s.stopped:
			return nil
		case <-tick.C:
		}
	}
}

// changed reports whether an adapter would act differently on b than on a.
func changed(a, b QueueStats) bool {
	if a.Connected != b.Connected || len(a.Lanes) != len(b.Lanes) {
		return true
	}
	for i := range a.Lanes {
		if a.Lanes[i].Lane != b.Lanes[i].Lane || a.Lanes[i].Throttle != b.Lanes[i].Throttle {
			return true
		}
	}
	return false
}
