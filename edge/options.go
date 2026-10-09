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

// WithEdgeToken sets the adapter's edge credential, attached to every call into the platform
// (default: ZQNT_EDGE_TOKEN). The platform refuses calls without one, except claim redemption.
func WithEdgeToken(token string) Option {
	return func(c *config) { c.auth.EdgeToken = token }
}

// WithPlatformPublicKey sets the platform's service public key the adapter's server verifies
// every command with (default: ZQNT_PLATFORM_PUBLIC_KEY / SERVICE_AUTH_PUBLIC_KEY).
func WithPlatformPublicKey(key string) Option {
	return func(c *config) { c.auth.PlatformPublicKey = key }
}

// WithoutPlatformAuth accepts commands from anyone who can reach the adapter's port. Local
// SITL/simulators only -- the same as ZQNT_EDGE_AUTH_DISABLED=true.
func WithoutPlatformAuth() Option {
	return func(c *config) { c.auth.Disabled = true }
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

// WithRemoteControlAddr dials remote-control (v3 EdgeGatewayService: command events and
// capability reports) at addr instead of the main endpoint. See WithConnectorAddr.
func WithRemoteControlAddr(addr string) Option {
	return func(c *config) { c.remoteControlAddr = addr }
}
