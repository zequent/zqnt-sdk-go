package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	controlv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/control/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"
	"google.golang.org/grpc"
)

// ErrManualControlClosed is returned by Send after Close.
var ErrManualControlClosed = errors.New("control: the manual control stream is closed")

// ManualControl is a stick-input session on one asset: roll, pitch, yaw and throttle from -1 to 1,
// and an optional gimbal pitch. The asset has to be in manual control first (flight.manual.enter).
// Send and Close may be called from different goroutines.
type ManualControl struct {
	sn     string
	stream grpc.ClientStreamingClient[controlv3.StreamManualControlRequest, controlv3.StreamManualControlResponse]

	mu       sync.Mutex
	named    bool
	closed   bool
	accepted int64
	err      error
}

// StreamManualControl opens a stick-input session on the asset. One session controls one asset;
// inputs go out in the order they are sent until Close or until ctx ends, which aborts the stream.
func (c *Client) StreamManualControl(ctx context.Context, sn string) (*ManualControl, error) {
	if sn == "" {
		return nil, errors.New("control: StreamManualControl: sn is required")
	}
	stream, err := c.grpc.StreamManualControl(ctx)
	if err != nil {
		return nil, fmt.Errorf("control: StreamManualControl(%s): %w", sn, err)
	}
	return &ManualControl{sn: sn, stream: stream}, nil
}

// Sn is the asset the session controls.
func (m *ManualControl) Sn() string {
	return m.sn
}

// Send sends one input. When the platform has refused the session (no adapter reachable for the
// asset, an asset of another organization) or the adapter dropped it, Send returns that error with
// its gRPC status, and so does every later Send and Close.
func (m *ManualControl) Send(input *edgev3.ManualControlInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		if m.err != nil {
			return m.err
		}
		return ErrManualControlClosed
	}
	req := &controlv3.StreamManualControlRequest{Input: input}
	if !m.named {
		req.Asset = &commonv3.AssetRef{Sn: m.sn}
		m.named = true
	}
	if err := m.stream.Send(req); err != nil {
		if errors.Is(err, io.EOF) {
			m.finish()
			if m.err != nil {
				return m.err
			}
			return ErrManualControlClosed
		}
		m.closed = true
		m.err = fmt.Errorf("control: StreamManualControl(%s): %w", m.sn, err)
		return m.err
	}
	return nil
}

// Close ends the session and returns the number of inputs the platform passed on to the asset. An
// error in the platform's answer is a *CommandError. Calling Close again returns the same answer.
func (m *ManualControl) Close() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.finish()
	}
	return m.accepted, m.err
}

func (m *ManualControl) finish() {
	m.closed = true
	resp, err := m.stream.CloseAndRecv()
	switch {
	case err != nil:
		m.err = fmt.Errorf("control: StreamManualControl(%s): %w", m.sn, err)
	case resp.GetError() != nil:
		m.err = &CommandError{Result: &capabilityv3.CommandResult{
			CommandId: "manual control on " + m.sn,
			State:     capabilityv3.CommandState_COMMAND_STATE_FAILED,
			Error:     resp.GetError(),
		}}
	default:
		m.accepted = resp.GetAcceptedInputs()
	}
}
