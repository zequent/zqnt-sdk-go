package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/zequent/zqnt-edge-sdk-go/v3/conformance"
)

func TestExampleConforms(t *testing.T) {
	events := &conformance.Recorder{}
	drone := newDrone(slog.New(slog.NewTextHandler(io.Discard, nil)))
	drone.events = events
	conformance.Run(t, drone, conformance.Options{Events: events, CompletionTimeout: 5 * time.Second})
}
