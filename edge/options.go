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
