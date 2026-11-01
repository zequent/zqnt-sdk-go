// Package simulation runs an Application of a Zequent 3.x platform on a virtual fleet and reads
// which way it went. The platform's own engine runs it, every command is answered from its
// published contract and the request's scenario, and no command leaves the platform. It is the
// client of zqnt.application_simulation.v3.SimulationService, served by mission-autonomy.
//
//	conn, err := cfg.Dial(cfg.MissionAutonomy)
//	trace, err := simulation.New(conn).Simulate(ctx, &simulationv3.StartSimulationRequest{
//		ApplicationId: applicationID,
//	}, time.Second, 2*time.Minute)
package simulation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	simulationv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/application_simulation/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ErrWaitTimedOut is returned by WaitForSimulation and Simulate when the run has not ended within
// the timeout; the run keeps going on the platform.
var ErrWaitTimedOut = errors.New("simulation: the run did not end within the timeout")

// Client wraps the generated SimulationServiceClient.
type Client struct {
	grpc simulationv3.SimulationServiceClient
}

// New wraps a connection to mission-autonomy, e.g. cfg.Dial(cfg.MissionAutonomy).
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: simulationv3.NewSimulationServiceClient(conn)}
}

// Final reports whether a run with this status has ended: SUCCEEDED, FAILED, CANCELLED or
// TIMED_OUT.
func Final(status simulationv3.SimulationStatus) bool {
	switch status {
	case simulationv3.SimulationStatus_SIMULATION_STATUS_SUCCEEDED,
		simulationv3.SimulationStatus_SIMULATION_STATUS_FAILED,
		simulationv3.SimulationStatus_SIMULATION_STATUS_CANCELLED,
		simulationv3.SimulationStatus_SIMULATION_STATUS_TIMED_OUT:
		return true
	}
	return false
}

// StartSimulation starts a simulated run and returns once it is running, with its execution id
// and status. A request without context.idempotency_key gets a new one; a start retried with the
// same key answers the run it started. The request is not changed. The error keeps its gRPC
// status: InvalidArgument for an unusable scenario or fleet, NotFound for an Application that is
// not the caller's organization's.
func (c *Client) StartSimulation(ctx context.Context, req *simulationv3.StartSimulationRequest) (*simulationv3.StartSimulationResponse, error) {
	if req.GetApplicationId() == "" {
		return nil, errors.New("simulation: StartSimulation: application id is required")
	}
	start := proto.Clone(req).(*simulationv3.StartSimulationRequest)
	if start.Context == nil {
		start.Context = &commonv3.RequestContext{}
	}
	if start.Context.RequestId == "" {
		start.Context.RequestId = newID("client-go-sdk-")
	}
	if start.Context.IdempotencyKey == "" {
		start.Context.IdempotencyKey = newID("client-go-sdk-sim-")
	}
	resp, err := c.grpc.StartSimulation(ctx, start)
	if err != nil {
		return nil, fmt.Errorf("simulation: StartSimulation(%s): %w", req.GetApplicationId(), err)
	}
	return resp, nil
}

// SimulationTrace returns the path the run took so far, or in full once it has ended. The error
// keeps its gRPC status: NotFound for a run that is not a simulation of the caller's organization.
func (c *Client) SimulationTrace(ctx context.Context, executionID string) (*simulationv3.SimulationTrace, error) {
	if executionID == "" {
		return nil, errors.New("simulation: SimulationTrace: execution id is required")
	}
	resp, err := c.grpc.GetSimulationTrace(ctx, &simulationv3.GetSimulationTraceRequest{
		Context:     &commonv3.RequestContext{RequestId: newID("client-go-sdk-")},
		ExecutionId: executionID,
	})
	if err != nil {
		return nil, fmt.Errorf("simulation: SimulationTrace(%s): %w", executionID, err)
	}
	return resp.GetTrace(), nil
}

// CancelSimulation stops a simulated run and returns its status, CANCELLING while its commands
// stop.
func (c *Client) CancelSimulation(ctx context.Context, executionID string) (simulationv3.SimulationStatus, error) {
	if executionID == "" {
		return simulationv3.SimulationStatus_SIMULATION_STATUS_UNSPECIFIED, errors.New("simulation: CancelSimulation: execution id is required")
	}
	resp, err := c.grpc.CancelSimulation(ctx, &simulationv3.CancelSimulationRequest{
		Context:     &commonv3.RequestContext{RequestId: newID("client-go-sdk-")},
		ExecutionId: executionID,
	})
	if err != nil {
		return simulationv3.SimulationStatus_SIMULATION_STATUS_UNSPECIFIED, fmt.Errorf("simulation: CancelSimulation(%s): %w", executionID, err)
	}
	return resp.GetStatus(), nil
}

// WaitForSimulation polls the trace every interval until the run has ended (see Final) and returns
// that trace, whatever the outcome. When timeout elapses first (0 = no limit beyond ctx) it returns
// ErrWaitTimedOut and the run keeps going; CancelSimulation stops it.
func (c *Client) WaitForSimulation(ctx context.Context, executionID string, interval, timeout time.Duration) (*simulationv3.SimulationTrace, error) {
	if interval <= 0 {
		return nil, errors.New("simulation: WaitForSimulation: interval must be positive")
	}
	ownDeadline := false
	if timeout > 0 {
		parent, bounded := ctx.Deadline()
		ownDeadline = !bounded || time.Now().Add(timeout).Before(parent)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, ErrWaitTimedOut)
		defer cancel()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		trace, err := c.SimulationTrace(ctx, executionID)
		if err != nil {
			if errors.Is(context.Cause(ctx), ErrWaitTimedOut) || ownDeadline && status.Code(err) == codes.DeadlineExceeded {
				return nil, fmt.Errorf("simulation: waiting for %s: %w", executionID, ErrWaitTimedOut)
			}
			return nil, err
		}
		if Final(trace.GetStatus()) {
			return trace, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("simulation: waiting for %s: %w", executionID, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// Simulate starts a simulated run and waits for its trace as WaitForSimulation does.
func (c *Client) Simulate(ctx context.Context, req *simulationv3.StartSimulationRequest, interval, timeout time.Duration) (*simulationv3.SimulationTrace, error) {
	started, err := c.StartSimulation(ctx, req)
	if err != nil {
		return nil, err
	}
	return c.WaitForSimulation(ctx, started.GetExecutionId(), interval, timeout)
}

func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
