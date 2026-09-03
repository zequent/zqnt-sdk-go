// Package connector is a client for ConnectorService's asset lookup, Scheduler CRUD (scheduler.go),
// and operational-policy (policy.go) surface, mirroring what client-java-sdk's Connector interface
// exposes for the same RPCs.
//
// No Skill Registry here (main/2.0.0-only — ObserveSkillContract/ListSkillContracts/
// SetSkillContractStatus/SetSkillContractPermissions don't exist in connector.proto at the 1.3.0
// wire contract this branch tracks). See zqnt-protos' README "Versioning" section.
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

func requestBase() *base.RequestBase {
	return &base.RequestBase{Tid: newTid(), Timestamp: timestamppb.Now()}
}

func newTid() string {
	return fmt.Sprintf("client-go-sdk-%d", time.Now().UnixNano())
}
