// Package edgelink holds the integration test of zqnt-edge-node against the platform's edge-link:
// the node enrolls with a claim code, streams telemetry through toxiproxy, and keeps every sample
// exactly once through a full outage, a lossy slow link, connection cuts and acknowledgements held
// back until it retransmits. It needs Docker and runs only with -tags integration:
//
//	go test -tags integration -count=1 -v -timeout 15m ./test/edgelink/
package edgelink
