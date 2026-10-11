package v3compat

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSwitchFallsBackForTenMinutesThenTriesV3Again(t *testing.T) {
	now := time.Unix(0, 0)
	s := &Switch{now: func() time.Time { return now }}
	if !s.Available() {
		t.Fatal("v3 must be tried first")
	}
	if !s.MarkUnavailable() {
		t.Fatal("first MarkUnavailable must report the change")
	}
	if s.MarkUnavailable() {
		t.Fatal("second MarkUnavailable must not report a change")
	}
	now = now.Add(RetryAfter - time.Second)
	if s.Available() {
		t.Fatal("v3 retried too early")
	}
	now = now.Add(time.Second)
	if !s.Available() {
		t.Fatal("v3 not retried after RetryAfter")
	}
}

func TestUnimplemented(t *testing.T) {
	if !Unimplemented(status.Error(codes.Unimplemented, "unknown service")) {
		t.Error("UNIMPLEMENTED not recognised")
	}
	if Unimplemented(status.Error(codes.Unavailable, "down")) || Unimplemented(errors.New("x")) || Unimplemented(nil) {
		t.Error("other errors must not count as UNIMPLEMENTED")
	}
}
