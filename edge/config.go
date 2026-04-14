// Package edgesdk is the root package of the edge-go-sdk.
// It provides [EdgeClient], the main entry point for integrators.
package edgesdk

import (
	"log/slog"
	"time"
)

// config holds the resolved configuration for an EdgeClient.
// It is populated from the required positional arguments and zero or more Options.
type config struct {
	endpoint    string
	sn          string
	timeout     time.Duration
	maxRetries  int
	assetType   string
	assetVendor string
	assetID     string
	logger      *slog.Logger
}

func defaultConfig(endpoint, sn string) *config {
	return &config{
		endpoint:   endpoint,
		sn:         sn,
		timeout:    30 * time.Second,
		maxRetries: 3,
		logger:     slog.Default(),
	}
}
