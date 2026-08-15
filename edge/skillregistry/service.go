// Package skillregistry lets an edge adapter self-report its own command contracts directly into
// the platform's persisted Skill Registry, instead of only ever being polled indirectly via
// EdgeAdapterService#GetCapabilities. It talks to the current ConnectorService — regenerated from
// the up-to-date proto schema under gen/connector/proto, independent of the older, submodule-pinned
// gen/proto used by the rest of this SDK (see this repo's README for why the two coexist).
package skillregistry

import (
	"context"
	"log/slog"

	base "github.com/Zequent/zqnt-edge-sdk-go/gen/common/base/proto"
	connectorpb "github.com/Zequent/zqnt-edge-sdk-go/gen/connector/proto"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/retry"
)

// Service is the client-side interface for the Skill Registry surface of ConnectorService.
type Service interface {
	// ObserveSkillContract upserts contract — new for a never-seen (command_id, schema_version)
	// pair, or refreshed content/last-seen for one already known.
	ObserveSkillContract(ctx context.Context, contract *connectorpb.SkillContractProtoDTO) (*connectorpb.SkillContractProtoDTO, error)
	// ListSkillContracts lists the whole registry, optionally filtered by status. When commandID is
	// non-empty, it instead returns that one command's full version history (status is then
	// ignored, matching the RPC's own semantics).
	ListSkillContracts(ctx context.Context, status *connectorpb.SkillContractStatus, commandID string) ([]*connectorpb.SkillContractProtoDTO, error)
}

// ServiceImpl is the gRPC-backed Service implementation.
type ServiceImpl struct {
	stub connectorpb.ConnectorServiceClient
	log  *slog.Logger
}

// NewServiceImpl creates a new Service implementation. conn should point at the same Connector
// backend as the rest of the SDK's connector.ServiceImpl.
func NewServiceImpl(stub connectorpb.ConnectorServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{stub: stub, log: log}
}

func newBase(sn string) *base.RequestBase {
	return &base.RequestBase{
		Tid:       protohelpers.GenerateTID(),
		Sn:        sn,
		Timestamp: protohelpers.Now(),
	}
}

func (s *ServiceImpl) ObserveSkillContract(ctx context.Context, contract *connectorpb.SkillContractProtoDTO) (*connectorpb.SkillContractProtoDTO, error) {
	req := &connectorpb.UpsertSkillContractRequest{Base: newBase(""), Contract: contract}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.SkillContractResponse, error) {
		return s.stub.ObserveSkillContract(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("ObserveSkillContract error", "error", resp.GetError())
		return nil, nil
	}
	return resp.GetContract(), nil
}

func (s *ServiceImpl) ListSkillContracts(ctx context.Context, status *connectorpb.SkillContractStatus, commandID string) ([]*connectorpb.SkillContractProtoDTO, error) {
	req := &connectorpb.ListSkillContractsRequest{Base: newBase(""), Status: status}
	if commandID != "" {
		req.CommandId = &commandID
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.SkillContractListResponse, error) {
		return s.stub.ListSkillContracts(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("ListSkillContracts error", "error", resp.GetError())
		return nil, nil
	}
	return resp.GetContracts(), nil
}
