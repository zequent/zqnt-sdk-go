package livedata

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	// zqntpbbuf message types (package _go → alias it)
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"

	// gRPC service stubs (package _gogrpc → alias it)
	zqntgrpc "buf.build/gen/go/zqnt/protos/grpc/go/_gogrpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	initialReconnectDelay = 2 * time.Second
	maxReconnectDelay     = 60 * time.Second
	maxReconnectAttempts  = 100
)

// streamEntry holds a single active client-streaming connection.
type streamEntry struct {
	stream zqntgrpc.LiveDataService_ProduceTelemetryClient
	cancel context.CancelFunc
}

// ServiceImpl is the gRPC-backed implementation of LiveDataService.
// It maintains one persistent bidirectional stream per device SN and
// reconnects automatically on transient failures.
type ServiceImpl struct {
	stub         zqntgrpc.LiveDataServiceClient
	mapper       *Mapper
	log          *slog.Logger
	mu           sync.RWMutex
	streams      map[string]*streamEntry
	attempts     map[string]int
	shuttingDown atomic.Bool
}

// NewServiceImpl creates a new LiveDataService implementation.
func NewServiceImpl(stub zqntgrpc.LiveDataServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{
		stub:     stub,
		mapper:   &Mapper{},
		log:      log,
		streams:  make(map[string]*streamEntry),
		attempts: make(map[string]int),
	}
}

func (s *ServiceImpl) ProduceTelemetryData(ctx context.Context, data *domains.TelemetryRequestData) error {
	if data == nil {
		return nil
	}
	req := s.mapper.Map(data)
	return s.ProduceTelemetry(ctx, data.SN, req)
}

func (s *ServiceImpl) ProduceTelemetry(ctx context.Context, deviceSN string, req *zqntpb.ProduceTelemetryRequest) error {
	if s.shuttingDown.Load() {
		s.log.Warn("cannot produce telemetry: service is shutting down", "sn", deviceSN)
		return nil
	}

	entry, err := s.getOrCreateStream(ctx, deviceSN)
	if err != nil || entry == nil {
		return err
	}

	if sendErr := entry.stream.Send(req); sendErr != nil {
		s.log.Error("error sending telemetry", "sn", deviceSN, "error", sendErr)
		s.removeStream(deviceSN)
		return sendErr
	}
	return nil
}

func (s *ServiceImpl) CloseStream(_ context.Context, deviceSN string) error {
	s.mu.Lock()
	entry, ok := s.streams[deviceSN]
	if ok {
		delete(s.streams, deviceSN)
		delete(s.attempts, deviceSN)
	}
	s.mu.Unlock()

	if entry != nil {
		entry.cancel()
		if _, err := entry.stream.CloseAndRecv(); err != nil {
			s.log.Warn("error closing stream", "sn", deviceSN, "error", err)
		}
		s.log.Info("closed telemetry stream", "sn", deviceSN)
	}
	return nil
}

func (s *ServiceImpl) CloseAllStreams(_ context.Context) error {
	s.mu.Lock()
	entries := make(map[string]*streamEntry, len(s.streams))
	for k, v := range s.streams {
		entries[k] = v
	}
	s.streams = make(map[string]*streamEntry)
	s.attempts = make(map[string]int)
	s.mu.Unlock()

	s.log.Info("closing all telemetry streams", "count", len(entries))
	for sn, entry := range entries {
		entry.cancel()
		if _, err := entry.stream.CloseAndRecv(); err != nil {
			s.log.Warn("error closing stream", "sn", sn, "error", err)
		}
	}
	s.log.Info("all telemetry streams closed")
	return nil
}

// Shutdown sets the shutting-down flag and closes all streams.
func (s *ServiceImpl) Shutdown(ctx context.Context) error {
	s.log.Info("LiveDataService shutdown initiated")
	s.shuttingDown.Store(true)
	return s.CloseAllStreams(ctx)
}

// ---- internal helpers -------------------------------------------------------

func (s *ServiceImpl) getOrCreateStream(ctx context.Context, deviceSN string) (*streamEntry, error) {
	s.mu.RLock()
	entry, ok := s.streams[deviceSN]
	s.mu.RUnlock()
	if ok {
		return entry, nil
	}

	if s.shuttingDown.Load() {
		return nil, nil
	}

	return s.createStream(ctx, deviceSN)
}

func (s *ServiceImpl) createStream(ctx context.Context, deviceSN string) (*streamEntry, error) {
	s.log.Info("creating gRPC telemetry stream", "sn", deviceSN)

	streamCtx, cancel := context.WithCancel(context.Background())
	stream, err := s.stub.ProduceTelemetry(streamCtx)
	if err != nil {
		cancel()
		s.log.Error("failed to create telemetry stream", "sn", deviceSN, "error", err)
		s.scheduleReconnect(deviceSN, 1)
		return nil, err
	}

	entry := &streamEntry{stream: stream, cancel: cancel}

	s.mu.Lock()
	s.streams[deviceSN] = entry
	s.attempts[deviceSN] = 0
	s.mu.Unlock()

	// Monitor the stream in the background for server-side errors.
	go s.monitorStream(ctx, deviceSN, entry, stream)

	return entry, nil
}

func (s *ServiceImpl) monitorStream(_ context.Context, deviceSN string, entry *streamEntry, stream zqntgrpc.LiveDataService_ProduceTelemetryClient) {
	_, err := stream.CloseAndRecv()
	if err == nil {
		s.log.Info("telemetry stream completed normally", "sn", deviceSN)
		s.removeStream(deviceSN)
		return
	}

	s.log.Error("telemetry stream error", "sn", deviceSN, "error", err)
	s.removeStream(deviceSN)

	if s.shuttingDown.Load() || !s.shouldReconnect(err) {
		return
	}

	s.mu.Lock()
	s.attempts[deviceSN]++
	attempt := s.attempts[deviceSN]
	s.mu.Unlock()

	if attempt <= maxReconnectAttempts {
		s.scheduleReconnect(deviceSN, attempt)
	} else {
		s.log.Warn("max reconnect attempts reached for device; manual recovery required", "sn", deviceSN, "attempts", attempt)
	}
}

func (s *ServiceImpl) removeStream(deviceSN string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, deviceSN)
}

func (s *ServiceImpl) scheduleReconnect(deviceSN string, attempt int) {
	if s.shuttingDown.Load() {
		return
	}
	delay := s.computeDelay(attempt)
	s.log.Info("scheduling reconnect for device", "sn", deviceSN, "delay", delay, "attempt", attempt)

	time.AfterFunc(delay, func() {
		if s.shuttingDown.Load() {
			return
		}
		s.mu.RLock()
		_, exists := s.streams[deviceSN]
		s.mu.RUnlock()
		if exists {
			return
		}

		if _, err := s.createStream(context.Background(), deviceSN); err != nil {
			s.log.Error("reconnect failed", "sn", deviceSN, "error", err)
		}
	})
}

func (s *ServiceImpl) shouldReconnect(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true
	}
	switch st.Code() {
	case codes.Unavailable,
		codes.DeadlineExceeded,
		codes.ResourceExhausted,
		codes.Internal,
		codes.Unknown:
		return true
	case codes.Unauthenticated,
		codes.PermissionDenied,
		codes.FailedPrecondition,
		codes.Unimplemented,
		codes.DataLoss:
		return false
	default:
		return true
	}
}

func (s *ServiceImpl) computeDelay(attempt int) time.Duration {
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	base := initialReconnectDelay * time.Duration(1<<shift)
	if base > maxReconnectDelay {
		base = maxReconnectDelay
	}
	jitter := time.Duration(rand.Int63n(int64(base)/4 + 1))
	return base + jitter
}
