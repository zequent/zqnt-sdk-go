package session

import (
	"sync"
	"time"
)

// Clock is the node's view of platform time: the local clock plus the offset the platform sent
// in its last Welcome. Until the first Welcome it is unsynced and envelopes say so.
type Clock struct {
	local func() time.Time

	mu     sync.Mutex
	offset time.Duration
	synced bool
}

// NewClock reads local time from local; nil uses time.Now.
func NewClock(local func() time.Time) *Clock {
	if local == nil {
		local = time.Now
	}
	return &Clock{local: local}
}

// Local is the node's own wall clock.
func (c *Clock) Local() time.Time { return c.local() }

// Now is the local clock corrected by the platform offset.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.local().Add(c.offset)
}

// Synced reports whether a platform offset has been applied.
func (c *Clock) Synced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced
}

// Offset is platform time minus local time.
func (c *Clock) Offset() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offset
}

// Sync applies an offset from the platform.
func (c *Clock) Sync(offset time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset, c.synced = offset, true
}
