package edgesdk

import (
	"log/slog"
	"time"
)

// Option is a functional option for configuring an [EdgeClient].
type Option func(*config)

// WithTimeout sets the per-call deadline applied to connector and mission-autonomy calls.
// Defaults to 30 seconds.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithMaxRetries sets the maximum number of retry attempts for transient gRPC errors.
// Defaults to 3.
func WithMaxRetries(n int) Option {
	return func(c *config) { c.maxRetries = n }
}

// WithAssetType sets the asset type string (e.g. "ASSET_TYPE_DOCK").
func WithAssetType(t string) Option {
	return func(c *config) { c.assetType = t }
}

// WithAssetVendor sets the asset vendor string (e.g. "DJI").
func WithAssetVendor(v string) Option {
	return func(c *config) { c.assetVendor = v }
}

// WithAssetID sets an optional asset ID.
func WithAssetID(id string) Option {
	return func(c *config) { c.assetID = id }
}

// WithLogger sets a custom slog.Logger for the SDK.
// Defaults to slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.logger = l }
}

// WithConnectorAddr dials ConnectorService at addr instead of the main endpoint passed to
// [NewEdgeClient]. Use this whenever connector isn't reachable at the same address as live-data/
// mission-autonomy -- true of every real deployment topology in this monorepo (see config.go's
// doc comment on why the single-endpoint default rarely applies as-is).
func WithConnectorAddr(addr string) Option {
	return func(c *config) { c.connectorAddr = addr }
}

// WithLiveDataAddr dials LiveDataService at addr instead of the main endpoint. See
// WithConnectorAddr.
func WithLiveDataAddr(addr string) Option {
	return func(c *config) { c.liveDataAddr = addr }
}

// WithMissionAutonomyAddr dials MissionAutonomyService at addr instead of the main endpoint. See
// WithConnectorAddr.
func WithMissionAutonomyAddr(addr string) Option {
	return func(c *config) { c.missionAutonomyAddr = addr }
}
