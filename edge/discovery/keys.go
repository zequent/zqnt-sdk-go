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
	edgeEndpointKeyPrefix = "edge-endpoints:" // + {vendor}
	edgeVendorKeyPrefix   = "edge-vendor:"    // + {sn}
)

// endpointKey returns the Redis key holding the EdgeEndpoint JSON blob for a vendor.
// Mirrors CacheKeys.EDGE_ENDPOINTS ("edge-endpoints:{vendor}") -- no "zqnt:" prefix; that was
// wrong here (fixed alongside this comment) and never matched the real Java-side key CacheKeys
// actually produces.
func endpointKey(vendor string) string {
	return fmt.Sprintf("%s%s", edgeEndpointKeyPrefix, vendor)
}

// vendorKey returns the Redis key mapping one SN to its vendor.
// Mirrors CacheKeys.EDGE_VENDOR ("edge-vendor:{sn}") -- no "zqnt:" prefix, same correction as
// endpointKey above.
func vendorKey(sn string) string {
	return fmt.Sprintf("%s%s", edgeVendorKeyPrefix, sn)
}
