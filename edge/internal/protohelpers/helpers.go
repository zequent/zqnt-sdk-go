// Package protohelpers provides small utilities shared across the SDK for
// building proto messages.
package protohelpers

import (
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Now returns the current time as a protobuf Timestamp.
func Now() *timestamppb.Timestamp {
	return timestamppb.New(time.Now())
}

// GenerateTID returns a new random transaction ID.
func GenerateTID() string {
	return uuid.NewString()
}
