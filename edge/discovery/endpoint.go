package discovery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// EdgeEndpoint is the JSON shape stored at CacheKeys.EDGE_ENDPOINTS -- field-for-field the same
// document utils/zqnt-utils's EdgeEndpointDTO serializes via Jackson (default bean-property
// naming: lowerCamelCase field names, enums as their .name() string), so either side can read
// what the other wrote.
type EdgeEndpoint struct {
	Endpoint    string `json:"endpoint"`
	Online      bool   `json:"online"`
	AssetType   string `json:"assetType"`
	AssetVendor string `json:"assetVendor"`
}

// Registrar registers/deregisters this process as the gRPC endpoint for a vendor, and maps
// individual asset SNs to that vendor, in the platform's shared Redis instance.
type Registrar struct {
	rdb *redis.Client
}

// NewRegistrar creates a Registrar against the given Redis connection (e.g. "localhost:6379";
// same instance connector/remote-control use -- REDIS_URL in core/docker-compose.local.yml).
func NewRegistrar(rdb *redis.Client) *Registrar {
	return &Registrar{rdb: rdb}
}

// RegisterEndpoint publishes this adapter's own reachable address as the live gRPC endpoint for
// vendor. endpoint is host:port as remote-control's GrpcEndpointRouter will dial it -- it must be
// reachable from wherever remote-control runs, not just from this process (e.g. the container/host
// address, not "localhost", when the two aren't on the same network namespace).
func (r *Registrar) RegisterEndpoint(ctx context.Context, vendor, assetType, endpoint string) error {
	blob, err := json.Marshal(EdgeEndpoint{
		Endpoint:    endpoint,
		Online:      true,
		AssetType:   assetType,
		AssetVendor: vendor,
	})
	if err != nil {
		return fmt.Errorf("discovery: marshal EdgeEndpoint: %w", err)
	}
	if err := r.rdb.Set(ctx, endpointKey(vendor), blob, 0).Err(); err != nil {
		return fmt.Errorf("discovery: register endpoint for vendor %s: %w", vendor, err)
	}
	return nil
}

// DeregisterEndpoint soft-deletes the vendor's endpoint (marks it offline, keeps the record for
// monitoring) -- mirrors CachingService.deregisterEdgeEndpoint, called from graceful shutdown.
func (r *Registrar) DeregisterEndpoint(ctx context.Context, vendor string) error {
	raw, err := r.rdb.Get(ctx, endpointKey(vendor)).Result()
	if err == redis.Nil {
		return nil // nothing to deregister
	}
	if err != nil {
		return fmt.Errorf("discovery: read endpoint for vendor %s: %w", vendor, err)
	}
	var ep EdgeEndpoint
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		return fmt.Errorf("discovery: unmarshal endpoint for vendor %s: %w", vendor, err)
	}
	ep.Online = false
	blob, err := json.Marshal(ep)
	if err != nil {
		return fmt.Errorf("discovery: marshal EdgeEndpoint: %w", err)
	}
	if err := r.rdb.Set(ctx, endpointKey(vendor), blob, 0).Err(); err != nil {
		return fmt.Errorf("discovery: deregister endpoint for vendor %s: %w", vendor, err)
	}
	return nil
}

// RegisterAssetVendor maps sn -> vendor so GrpcEndpointRouter.getEndpointForAsset(sn) can resolve
// which vendor's (and therefore which adapter process's) endpoint owns this device. Call once per
// simulated device after it's registered with Connector.
func (r *Registrar) RegisterAssetVendor(ctx context.Context, sn, vendor string) error {
	if err := r.rdb.Set(ctx, vendorKey(sn), vendor, 0).Err(); err != nil {
		return fmt.Errorf("discovery: register vendor for sn %s: %w", sn, err)
	}
	return nil
}
