// Package retry provides a generic exponential backoff helper for gRPC calls.
package retry

import (
	"context"
	"math/rand"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	InitialDelay = 1 * time.Second
	MaxDelay     = 30 * time.Second
	MaxAttempts  = 5
)

// retryableCodes are gRPC status codes that warrant a retry attempt.
var retryableCodes = map[codes.Code]bool{
	codes.Unavailable:       true,
	codes.DeadlineExceeded:  true,
	codes.ResourceExhausted: true,
	codes.Internal:          true,
	codes.Unknown:           true,
}

// ShouldRetry reports whether the error is a transient gRPC error worth retrying.
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true
	}
	return retryableCodes[st.Code()]
}

// ComputeDelay returns the jittered backoff duration for a given attempt number (1-based).
func ComputeDelay(attempt int) time.Duration {
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	base := InitialDelay * time.Duration(1<<shift)
	if base > MaxDelay {
		base = MaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(base) / 4))
	return base + jitter
}

// Do calls fn with exponential backoff, honouring ctx cancellation.
// fn receives a fresh context derived from ctx on each attempt.
// Returns the result of the first successful call or the last error.
func Do[RES any](ctx context.Context, fn func(context.Context) (RES, error)) (RES, error) {
	var zero RES
	var lastErr error

	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !ShouldRetry(err) {
			return zero, err
		}

		if attempt == MaxAttempts {
			break
		}

		delay := ComputeDelay(attempt)
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}

	return zero, lastErr
}
