package connector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	commonpb "github.com/zequent/zqnt-api-go/v3/gen/common/proto"
	connectorpb "github.com/zequent/zqnt-api-go/v3/gen/connector/proto"
	"github.com/zequent/zqnt-edge-sdk-go/v3/adapter/domains"
	"github.com/zequent/zqnt-edge-sdk-go/v3/internal/protohelpers"
	"github.com/zequent/zqnt-edge-sdk-go/v3/internal/retry"
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

// Deprecated: see ConnectorService.RegisterAsset.
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

func (s *ServiceImpl) RedeemAssetClaim(ctx context.Context, code string, asset *domains.AssetDTO) (*domains.AssetDTO, error) {
	if code == "" || asset == nil || asset.SN == nil || *asset.SN == "" {
		return nil, errors.New("RedeemAssetClaim: a code and the asset's sn are required")
	}
	req := &connectorpb.RedeemAssetClaimRequest{Base: newBase(*asset.SN), Code: code, Asset: s.mapper.AssetToProto(asset)}
	// Not retried: a redemption that landed but lost its response would be refused as spent.
	resp, err := s.stub.RedeemAssetClaim(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() || resp.GetAsset() == nil {
		s.log.Error("claim code refused: it may be unknown, expired, revoked, used up or not valid for this kind of "+
			"device; the platform does not say which. Create a new code in the console and try again.", "sn", *asset.SN)
		return nil, nil
	}
	s.log.Info("claim redeemed, asset created", "sn", *asset.SN)
	return s.mapper.AssetFromProto(resp.GetAsset()), nil
}

func (s *ServiceImpl) DescribeAssetClaim(ctx context.Context, code string) (string, error) {
	if code == "" {
		return "", errors.New("DescribeAssetClaim: a code is required")
	}
	req := &connectorpb.DescribeAssetClaimRequest{Base: newBase(""), Code: code}
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.AssetClaimDescriptionResponse, error) {
		return s.stub.DescribeAssetClaim(c, req)
	})
	if err != nil {
		return "", err
	}
	if resp.GetHasErrors() || resp.OrganizationName == nil {
		s.log.Warn("claim code could not be resolved to an organization: it may be unknown, expired, revoked or used up")
		return "", nil
	}
	return resp.GetOrganizationName(), nil
}

func (s *ServiceImpl) EnsureAsset(ctx context.Context, asset *domains.AssetDTO, claimCode string) (*domains.AssetDTO, error) {
	if asset == nil || asset.SN == nil || *asset.SN == "" {
		return nil, errors.New("EnsureAsset: the asset's sn is required")
	}
	existing, err := s.GetAssetBySN(ctx, *asset.SN)
	if err != nil || existing != nil {
		return existing, err
	}
	if claimCode == "" {
		s.log.Warn("no asset registered for this sn and no claim code to pair one with: create the asset in the "+
			"console, or pair the device with a code (ZQNT_CLAIM_CODE)", "sn", *asset.SN)
		return nil, nil
	}
	return s.RedeemAssetClaim(ctx, claimCode, asset)
}

// DeRegisterAsset deletes the asset with this sn. Connector deletes by the request's own sn, so it
// must be sent: it used to be dropped, every call asked to delete sn "", nothing was ever deleted,
// and the refusal came back as (false, nil) -- a removal that looked like it had worked.
func (s *ServiceImpl) DeRegisterAsset(ctx context.Context, sn string) (bool, error) {
	if sn == "" {
		return false, errors.New("DeRegisterAsset: sn is required")
	}
	req := newBase(sn)
	resp, err := retry.Do(ctx, func(c context.Context) (*connectorpb.ConnectorResponse, error) {
		return s.stub.DeregisterAsset(c, req)
	})
	if err != nil {
		return false, err
	}
	if resp.GetHasErrors() {
		return false, fmt.Errorf("DeRegisterAsset %s: %s", sn, resp.GetError().GetErrorMessage())
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
