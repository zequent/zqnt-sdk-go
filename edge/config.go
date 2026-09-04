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

	// connectorAddr/liveDataAddr/missionAutonomyAddr override endpoint for that one backend
	// service's own connection. Empty means "dial endpoint" (the original single-address
	// behavior, correct only when something in front of endpoint actually multiplexes all three
	// services -- connector, live-data and mission-autonomy are three independent Quarkus
	// services on three independent ports in every real deployment topology this monorepo has
	// (see core/docker-compose.local.yml's *_SERVICE_HOST/*_SERVICE_PORT env vars, and every
	// existing adapter's application.properties), so a real integrator almost always needs these
	// three set explicitly via WithConnectorAddr/WithLiveDataAddr/WithMissionAutonomyAddr.
	connectorAddr       string
	liveDataAddr        string
	missionAutonomyAddr string
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
