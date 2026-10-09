package livedata

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// ErrReconnecting is returned for an item sent while a broken stream waits for its next attempt.
var ErrReconnecting = errors.New("livedata: stream is reconnecting")

const terminalStatusWait = 2 * time.Second

type countedResponse interface {
	GetAccepted() int64
	GetRejected() int64
}

type openStream[Req, Resp any] func(ctx context.Context) (grpc.ClientStreamingClient[Req, Resp], error)

type streamConn[Req, Resp any] struct {
	stream grpc.ClientStreamingClient[Req, Resp]
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// ingestStream is one long-lived client stream. It opens on the first send, watches the server's
// end of it, and after a failure reopens no earlier than an exponential backoff allows; an item
// sent before that gets ErrReconnecting.
type ingestStream[Req, Resp any] struct {
	name string
	open openStream[Req, Resp]
	log  *slog.Logger

	// onEnd is called with the status the server ended the stream with.
	onEnd func(error)

	mu      sync.Mutex
	conn    *streamConn[Req, Resp]
	attempt int
	retryAt time.Time
	closed  bool
	now     func() time.Time
}

func newIngestStream[Req, Resp any](name string, open openStream[Req, Resp], log *slog.Logger, onEnd func(error)) *ingestStream[Req, Resp] {
	return &ingestStream[Req, Resp]{name: name, open: open, log: log, onEnd: onEnd, now: time.Now}
}

// send hands req to the stream. A failure returns the status the server ended the stream with
// when it has one, so a caller can tell UNIMPLEMENTED from a broken connection.
func (st *ingestStream[Req, Resp]) send(req *Req) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errors.New("livedata: " + st.name + " stream is closed")
	}
	if st.conn == nil {
		if st.now().Before(st.retryAt) {
			return ErrReconnecting
		}
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := st.open(ctx)
		if err != nil {
			cancel()
			st.backoff()
			return err
		}
		st.conn = &streamConn[Req, Resp]{stream: stream, cancel: cancel, done: make(chan struct{})}
		go st.watch(st.conn)
	}
	conn := st.conn
	if err := conn.stream.Send(req); err != nil {
		select {
		case <-conn.done:
			if conn.err != nil {
				err = conn.err
			}
		case <-time.After(terminalStatusWait):
		}
		if st.conn == conn {
			st.drop(conn)
			st.backoff()
		}
		return err
	}
	st.attempt = 0
	return nil
}

func (st *ingestStream[Req, Resp]) watch(conn *streamConn[Req, Resp]) {
	resp := new(Resp)
	err := conn.stream.RecvMsg(resp)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	conn.err = err
	close(conn.done)

	if c, ok := any(resp).(countedResponse); ok && err == nil {
		st.log.Debug("stream closed", "stream", st.name, "accepted", c.GetAccepted(), "rejected", c.GetRejected())
	}
	st.mu.Lock()
	ended := st.conn == conn
	if ended {
		st.drop(conn)
		if err != nil {
			st.backoff()
		}
	}
	closed := st.closed
	st.mu.Unlock()
	if err != nil && !closed {
		if _, isStatus := status.FromError(err); isStatus {
			st.log.Warn("stream ended", "stream", st.name, "error", err)
		}
		if st.onEnd != nil {
			st.onEnd(err)
		}
	}
}

func (st *ingestStream[Req, Resp]) drop(conn *streamConn[Req, Resp]) {
	if st.conn == conn {
		st.conn = nil
	}
	conn.cancel()
}

func (st *ingestStream[Req, Resp]) backoff() {
	st.attempt++
	shift := min(st.attempt-1, 5)
	delay := min(initialReconnectDelay*time.Duration(1<<shift), maxReconnectDelay)
	st.retryAt = st.now().Add(delay)
}

// reset forgets a pending backoff, e.g. after switching back from a fallback.
func (st *ingestStream[Req, Resp]) reset() {
	st.mu.Lock()
	st.attempt, st.retryAt = 0, time.Time{}
	st.mu.Unlock()
}

// close half-closes the stream so the server answers with its counts, then releases it.
func (st *ingestStream[Req, Resp]) close() {
	st.mu.Lock()
	st.closed = true
	conn := st.conn
	st.conn = nil
	st.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.stream.CloseSend()
	select {
	case <-conn.done:
	case <-time.After(terminalStatusWait):
	}
	conn.cancel()
}
