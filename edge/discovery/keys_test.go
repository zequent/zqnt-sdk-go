package discovery

import "testing"

// TestEndpointVendorKeys guards the exact key format com.zqnt.utils.caching.CacheKeys reads on the
// 2.0.0 line. This has been wrong in both directions: the keys originally carried a "zqnt:" prefix
// CacheKeys didn't have on the 1.3.x line, and dropping it (correct then) left them wrong again
// once the 2.0.0 line reinstated it. Either way the symptom is the same and gives no hint of a key
// mismatch -- the adapter registers with Connector and streams telemetry, while every command
// returns "Asset not connected: <sn>".
func TestEndpointVendorKeys(t *testing.T) {
	if got, want := endpointKey("ASSET_VENDOR_MAVLINK"), "zqnt:edge-endpoints:ASSET_VENDOR_MAVLINK"; got != want {
		t.Errorf("endpointKey() = %q, want %q", got, want)
	}
	if got, want := vendorKey("SIM-DRONE-001"), "zqnt:edge-vendor:SIM-DRONE-001"; got != want {
		t.Errorf("vendorKey() = %q, want %q", got, want)
	}
}
