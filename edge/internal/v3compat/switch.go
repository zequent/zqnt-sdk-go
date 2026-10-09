// Package v3compat decides when to use a v3 platform service and when to fall back to v2.
package v3compat

import (
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetryAfter is how long a v3 service that answered UNIMPLEMENTED is left alone before the SDK
// tries it again -- the same as core's EdgeCommandGateway.
const RetryAfter = 10 * time.Minute

// Switch remembers, per process, that the platform does not serve a v3 service yet.
type Switch struct {
	mu    sync.Mutex
	until time.Time
	now   func() time.Time
}

func (s *Switch) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Available reports whether the v3 service should be tried.
func (s *Switch) Available() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.clock().Before(s.until)
}

// MarkUnavailable switches to v2 for RetryAfter. It reports whether the switch was on v3 until
// now, so a caller logs the change once.
func (s *Switch) MarkUnavailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	wasAvailable := !now.Before(s.until)
	s.until = now.Add(RetryAfter)
	return wasAvailable
}

// Unimplemented reports whether err says the platform does not serve the called method.
func Unimplemented(err error) bool {
	return status.Code(err) == codes.Unimplemented
}
