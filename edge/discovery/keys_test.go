package discovery

import "testing"

// TestEndpointVendorKeys_NoZqntPrefix guards against a real bug: these keys previously carried a
// "zqnt:" prefix that com.zqnt.utils.caching.CacheKeys (the real, authoritative Java-side format
// connector-service/remote-control-service actually read/write) never had -- an adapter built on
// this SDK registered its Asset with Connector but was never actually reachable, because
// GrpcEndpointRouter's SN -> vendor -> endpoint lookup checked a different key than this package
// wrote to.
func TestEndpointVendorKeys_NoZqntPrefix(t *testing.T) {
	if got, want := endpointKey("ASSET_VENDOR_MAVLINK"), "edge-endpoints:ASSET_VENDOR_MAVLINK"; got != want {
		t.Errorf("endpointKey() = %q, want %q", got, want)
	}
	if got, want := vendorKey("SIM-DRONE-001"), "edge-vendor:SIM-DRONE-001"; got != want {
		t.Errorf("vendorKey() = %q, want %q", got, want)
	}
}
