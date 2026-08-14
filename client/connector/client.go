// Package connector is a client for ConnectorService's asset lookup and Skill Registry surface —
// the persisted, de-duplicated capability catalog (independent of which devices are currently
// connected), mirroring what client-java-sdk's Connector interface exposes for the same RPCs.
package connector

import (
	"context"
	"fmt"
	"time"

	asset "github.com/Zequent/zqnt-client-sdk-go/gen/common/asset/proto"
	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client wraps ConnectorServiceClient, converting the wire-level has_errors/error convention into
// an idiomatic Go error return.
type Client struct {
	grpc connectorpb.ConnectorServiceClient
}

// New wraps an existing gRPC connection — dial it yourself with your own retry/TLS policy.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: connectorpb.NewConnectorServiceClient(conn)}
}

// GetAssetBySn looks up the asset registered under sn. Returns (nil, nil) if not found.
func (c *Client) GetAssetBySn(ctx context.Context, sn string) (*asset.AssetProtoDTO, error) {
	resp, err := c.grpc.GetAssetBySn(ctx, &base.RequestBase{Tid: newTid(), Sn: sn, Timestamp: timestamppb.Now()})
	if err != nil {
		return nil, fmt.Errorf("connector: GetAssetBySn(%s): %w", sn, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: GetAssetBySn(%s): %s", sn, resp.GetError().GetErrorMessage())
	}
	return resp.GetAsset(), nil
}

// ObserveSkillContract upserts contract into the Skill Registry — new for a never-seen
// (command_id, schema_version) pair, or refreshed content/last-seen for one already known.
func (c *Client) ObserveSkillContract(ctx context.Context, contract *connectorpb.SkillContractProtoDTO) (*connectorpb.SkillContractProtoDTO, error) {
	resp, err := c.grpc.ObserveSkillContract(ctx, &connectorpb.UpsertSkillContractRequest{
		Base: requestBase(), Contract: contract,
	})
	if err != nil {
		return nil, fmt.Errorf("connector: ObserveSkillContract(%s): %w", contract.GetCommandId(), err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: ObserveSkillContract(%s): %s", contract.GetCommandId(), resp.GetError().GetErrorMessage())
	}
	return resp.GetContract(), nil
}

// ListSkillContracts lists the whole registry, optionally filtered by status. When commandId is
// non-empty, it instead returns that one command's full version history (status is then ignored,
// matching the RPC's own semantics) — pass status=nil, commandId="" for status-filtered use.
func (c *Client) ListSkillContracts(ctx context.Context, status *connectorpb.SkillContractStatus, commandID string) ([]*connectorpb.SkillContractProtoDTO, error) {
	req := &connectorpb.ListSkillContractsRequest{Base: requestBase(), Status: status}
	if commandID != "" {
		req.CommandId = &commandID
	}
	resp, err := c.grpc.ListSkillContracts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("connector: ListSkillContracts: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: ListSkillContracts: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetContracts(), nil
}

// SetSkillContractStatus sets a skill contract's lifecycle status.
func (c *Client) SetSkillContractStatus(ctx context.Context, id string, status connectorpb.SkillContractStatus) (*connectorpb.SkillContractProtoDTO, error) {
	resp, err := c.grpc.SetSkillContractStatus(ctx, &connectorpb.SetSkillContractStatusRequest{
		Base: requestBase(), Id: id, Status: status,
	})
	if err != nil {
		return nil, fmt.Errorf("connector: SetSkillContractStatus(%s): %w", id, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: SetSkillContractStatus(%s): %s", id, resp.GetError().GetErrorMessage())
	}
	return resp.GetContract(), nil
}

// SetSkillContractPermissions replaces (not merges) a skill contract's required_permissions.
// Declarative only — the platform has no user-level auth/RBAC system yet, so nothing currently
// enforces this.
func (c *Client) SetSkillContractPermissions(ctx context.Context, id string, requiredPermissions []string) (*connectorpb.SkillContractProtoDTO, error) {
	resp, err := c.grpc.SetSkillContractPermissions(ctx, &connectorpb.SetSkillContractPermissionsRequest{
		Base: requestBase(), Id: id, RequiredPermissions: requiredPermissions,
	})
	if err != nil {
		return nil, fmt.Errorf("connector: SetSkillContractPermissions(%s): %w", id, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: SetSkillContractPermissions(%s): %s", id, resp.GetError().GetErrorMessage())
	}
	return resp.GetContract(), nil
}

func requestBase() *base.RequestBase {
	return &base.RequestBase{Tid: newTid(), Timestamp: timestamppb.Now()}
}

func newTid() string {
	return fmt.Sprintf("client-go-sdk-%d", time.Now().UnixNano())
}
