package link

import (
	"sync"
	"time"
)

// StateFeed is a ready-made State channel for drivers: Publish never blocks, a reader that falls
// behind sees the latest state, and Close closes the channel.
type StateFeed struct {
	mu      sync.Mutex
	ch      chan StateChange
	current StateChange
	closed  bool
}

// NewStateFeed starts in initial with no change pending.
func NewStateFeed(initial LinkState, reason string) *StateFeed {
	return &StateFeed{
		ch:      make(chan StateChange, 1),
		current: StateChange{State: initial, Reason: reason, At: time.Now()},
	}
}

// Publish records a new state; publishing the current state and reason again is a no-op.
func (f *StateFeed) Publish(state LinkState, reason string) { f.PublishConn(state, reason, 0) }

// PublishConn records a new state of connection conn (Handshake.Conn).
func (f *StateFeed) PublishConn(state LinkState, reason string, conn uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || (f.current.State == state && f.current.Reason == reason && f.current.Conn == conn) {
		return
	}
	f.current = StateChange{State: state, Reason: reason, At: time.Now(), Conn: conn}
	select {
	case <-f.ch:
	default:
	}
	f.ch <- f.current
}

// Current is the latest published state.
func (f *StateFeed) Current() StateChange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

// C is the channel State returns.
func (f *StateFeed) C() <-chan StateChange { return f.ch }

// Close closes the channel; later Publish calls are ignored.
func (f *StateFeed) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.ch)
	}
}
