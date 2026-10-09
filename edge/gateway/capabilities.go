// Package gateway reports an adapter's capabilities to the platform.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	adaptergrpc "github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/grpc"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/retry"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/v3compat"
	commonpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/base/proto"
	devicecontrolpb "github.com/zequent/zqnt-utils-golang/v2/gen/devicecontrol/contracts/proto"
	remotecontrolpb "github.com/zequent/zqnt-utils-golang/v2/gen/remotecontrol/proto"
	capabilityv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/capability/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"

	"google.golang.org/protobuf/proto"
)

// CapabilityReporter sends the full capability snapshot of an asset to the platform: over v3
// EdgeGatewayService.ReportCapabilities, or v2 RemoteControlService.ReportAssetRuntime while the
// platform does not serve v3.
type CapabilityReporter struct {
	v3  edgev3.EdgeGatewayServiceClient
	v2  remotecontrolpb.RemoteControlServiceClient
	log *slog.Logger
	sw  v3compat.Switch
}

// NewCapabilityReporter creates a reporter; v2 may be nil when there is no fallback.
func NewCapabilityReporter(v3 edgev3.EdgeGatewayServiceClient, v2 remotecontrolpb.RemoteControlServiceClient, log *slog.Logger) *CapabilityReporter {
	return &CapabilityReporter{v3: v3, v2: v2, log: log}
}

// Report sends caps and returns the revision the platform accepted.
func (r *CapabilityReporter) Report(ctx context.Context, caps *domains.CurrentCapabilities) (string, error) {
	set := adaptergrpc.CapabilitySetV3(caps, r.log)
	set.Revision = revision(set)
	if r.sw.Available() {
		resp, err := retry.Do(ctx, func(c context.Context) (*edgev3.ReportCapabilitiesResponse, error) {
			return r.v3.ReportCapabilities(c, &edgev3.ReportCapabilitiesRequest{Capabilities: set})
		})
		if err == nil {
			return resp.GetAcceptedRevision(), nil
		}
		if !v3compat.Unimplemented(err) || r.v2 == nil {
			return "", err
		}
		if r.sw.MarkUnavailable() {
			r.log.Warn("platform does not serve zqnt.edge.v3.EdgeGatewayService; capabilities go over v2 ReportAssetRuntime",
				"retry_after", v3compat.RetryAfter)
		}
	}
	req := &devicecontrolpb.ReportAssetRuntimeRequest{
		Base:         &commonpb.RequestBase{Tid: protohelpers.GenerateTID(), Sn: caps.SN, Timestamp: protohelpers.Now()},
		AssetSn:      caps.SN,
		Revision:     set.Revision,
		ObservedAt:   set.ObservedAt,
		Capabilities: adaptergrpc.CapabilitiesV2(caps, r.log),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*devicecontrolpb.ReportAssetRuntimeResponse, error) {
		return r.v2.ReportAssetRuntime(c, req)
	})
	if err != nil {
		return "", err
	}
	if resp.GetHasErrors() {
		return "", errors.New("report asset runtime refused: " + resp.GetError().GetErrorMessage())
	}
	return resp.GetAcceptedRevision(), nil
}

// revision identifies the snapshot's content, so the platform can tell a change from a repeat.
func revision(set *capabilityv3.CapabilitySet) string {
	content := &capabilityv3.CapabilitySet{
		AssetSn:         set.GetAssetSn(),
		AssetType:       set.GetAssetType(),
		Capabilities:    set.GetCapabilities(),
		TelemetryFields: set.GetTelemetryFields(),
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(content)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}
