// Package discovery registers this adapter process with the platform's edge-endpoint routing
// layer so remote-control's GrpcEndpointRouter (core/services/remote-control, backed by
// utils/zqnt-utils's shared GrpcEndpointRouter) can find and dial this adapter's gRPC server.
//
// This is Redis state, not a gRPC RPC: every existing Java/Python edge adapter (see
// adapters/dji-adapter's Startup.java/OsdSubscriber.java, the reference implementation this
// package mirrors field-for-field) writes directly to the same shared Redis instance connector
// and remote-control read from, using the key format below (utils/zqnt-utils's
// com.zqnt.utils.caching.CacheKeys). edge-go-sdk had no equivalent before this package -- without
// it, an adapter built on this SDK registers its Asset with Connector but is never actually
// reachable: GrpcEndpointRouter.getEndpointForAsset resolves SN -> vendor -> endpoint through
// exactly these two keys and fails both lookups otherwise.
//
// Routing is per-vendor, not per-device: GrpcEndpointRouter.getStubForAsset(sn) resolves the SN's
// vendor, then dials the *one* endpoint registered for that vendor. One adapter process serves
// every device of a given vendor over a single gRPC listener, routing internally by the sn already
// threaded through every EdgeAdapter method -- exactly the "one process, many devices" model this
// package (and the simulator built on it) follows.
package discovery

import "fmt"

const (
	edgeEndpointKeyPrefix = "zqnt:edge-endpoints:" // + {vendor}
	edgeVendorKeyPrefix   = "zqnt:edge-vendor:"    // + {sn}
)

// The "zqnt:" prefix is not cosmetic and has moved once: com.zqnt.utils.caching.CacheKeys carries
// it on the 2.0.0 line (EDGE_ENDPOINTS "zqnt:edge-endpoints:{vendor}", EDGE_VENDOR
// "zqnt:edge-vendor:{sn}"), and did not on the 1.3.x line, where this package correctly dropped
// it. These keys must match whichever platform line the adapter is deployed against exactly:
// GrpcEndpointRouter.getEndpointForAsset resolves sn -> vendor -> endpoint through these two
// lookups and fails both otherwise. The failure is quiet and misleading -- the adapter registers
// its Asset with Connector and streams telemetry perfectly happily, while every command sent to it
// comes back "Asset not connected: <sn>", because nothing it wrote is at a key the platform reads.
//
// Verified against a running 2.0.0 stack, not read off the source: with the unprefixed keys,
// remote-control's own GetCapabilities returned "Asset not connected"; with these, it returns the
// device's full capability snapshot.

// endpointKey returns the Redis key holding the EdgeEndpoint JSON blob for a vendor.
// Mirrors CacheKeys.EDGE_ENDPOINTS.
func endpointKey(vendor string) string {
	return fmt.Sprintf("%s%s", edgeEndpointKeyPrefix, vendor)
}

// vendorKey returns the Redis key mapping one SN to its vendor. Mirrors CacheKeys.EDGE_VENDOR.
func vendorKey(sn string) string {
	return fmt.Sprintf("%s%s", edgeVendorKeyPrefix, sn)
}
