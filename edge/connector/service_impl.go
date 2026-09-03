package connector

import (
	"context"
	"log/slog"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/retry"
	commonpb "github.com/zequent/zqnt-utils-golang/gen/common/proto"
	connectorpb "github.com/zequent/zqnt-utils-golang/gen/connector/proto"
)

// ServiceImpl is the gRPC-backed ConnectorService implementation.
type ServiceImpl struct {
	stub   connectorpb.ConnectorServiceClient
	mapper *Mapper
	log    *slog.Logger
}

// NewServiceImpl creates a new ConnectorService implementation.
func NewServiceImpl(stub connectorpb.ConnectorServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{stub: stub, mapper: &Mapper{}, log: log}
}

// ---- helpers ----------------------------------------------------------------

func newBase(sn string) *commonpb.RequestBase {
	return &commonpb.RequestBase{
		Tid:       protohelpers.GenerateTID(),
		Sn:        sn,
		Timestamp: protohelpers.Now(),
	}
}

// ---- Asset operations -------------------------------------------------------

func (s *ServiceImpl) GetAssetBySN(ctx context.Context, sn string) (*domains.AssetDTO, error) {
	req := newBase(sn)
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.GetAssetBySn(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetAssetBySN error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAsset()), nil
}

func (s *ServiceImpl) GetAssetByID(ctx context.Context, id string) (*domains.AssetDTO, error) {
	req := &connectorpb.ConnectorGetAssetByIdRequest{Base: newBase(""), AssetId: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.GetAssetById(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetAssetByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAsset()), nil
}

func (s *ServiceImpl) GetSubAssetBySN(ctx context.Context, sn string) (*domains.SubAssetDTO, error) {
	req := newBase(sn)
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.GetSubAssetBySn(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetSubAssetBySN error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.SubAssetFromProto(resp.GetSubAsset()), nil
}

// UpdateAsset replaces every mutable field on the asset (an empty/unset update_mask on the wire
// means "replace all", matching this method's previous behavior before update_mask existed).
func (s *ServiceImpl) UpdateAsset(ctx context.Context, id string, asset *domains.AssetDTO) (*domains.AssetDTO, error) {
	sn := ""
	if asset.SN != nil {
		sn = *asset.SN
	}
	req := &connectorpb.ConnectorUpdateAssetRequest{
		Base:    newBase(sn),
		AssetId: id,
		Asset:   s.mapper.AssetToProto(asset),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.UpdateAsset(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("UpdateAsset error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAsset()), nil
}

func (s *ServiceImpl) RegisterAsset(ctx context.Context, asset *domains.AssetDTO) (*domains.AssetDTO, error) {
	sn := ""
	if asset.SN != nil {
		sn = *asset.SN
	}
	req := &connectorpb.ConnectorRegisterAssetRequest{
		Base:  newBase(sn),
		Asset: s.mapper.AssetToProto(asset),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.RegisterAsset(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("RegisterAsset error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAsset()), nil
}

func (s *ServiceImpl) DeRegisterAsset(ctx context.Context, _ string) (bool, error) {
	req := newBase("")
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.DeregisterAsset(c, req)
	})
	if err != nil {
		return false, err
	}
	if resp.GetHasErrors() {
		s.log.Error("DeRegisterAsset error", "error", resp.GetError())
		return false, nil
	}
	return true, nil
}

// ---- Organization -------------------------------------------------------------

func (s *ServiceImpl) GetOrganizationByID(ctx context.Context, _ string) (*domains.OrganizationDTO, error) {
	req := &connectorpb.ConnectorGetOrganizationRequest{Base: newBase("")}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.GetOrganization(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetOrganizationByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.OrgFromProto(resp.GetOrganization()), nil
}
