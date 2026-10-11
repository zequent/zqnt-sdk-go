package connector

import (
	"context"
	"net"
	"sync"
	"testing"

	assetpb "github.com/zequent/zqnt-api-go/v3/gen/common/asset/proto"
	commonpb "github.com/zequent/zqnt-api-go/v3/gen/common/proto"
	connectorpb "github.com/zequent/zqnt-api-go/v3/gen/connector/proto"
	"github.com/zequent/zqnt-edge-sdk-go/v3/adapter/domains"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type fakeConnector struct {
	connectorpb.UnimplementedConnectorServiceServer
	mu       sync.Mutex
	assets   map[string]*assetpb.AssetProtoDTO
	codes    map[string]string
	redeemed []*connectorpb.RedeemAssetClaimRequest
}

func (f *fakeConnector) GetAssetBySn(_ context.Context, req *commonpb.RequestBase) (*connectorpb.ConnectorResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.assets[req.GetSn()]; ok {
		return &connectorpb.ConnectorResponse{Response: &connectorpb.ConnectorResponse_Asset{Asset: a}}, nil
	}
	return &connectorpb.ConnectorResponse{HasErrors: proto.Bool(true)}, nil
}

func (f *fakeConnector) RedeemAssetClaim(_ context.Context, req *connectorpb.RedeemAssetClaimRequest) (*connectorpb.ConnectorResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redeemed = append(f.redeemed, req)
	org, ok := f.codes[req.GetCode()]
	if !ok {
		return &connectorpb.ConnectorResponse{HasErrors: proto.Bool(true)}, nil
	}
	delete(f.codes, req.GetCode())
	a := proto.Clone(req.GetAsset()).(*assetpb.AssetProtoDTO)
	a.Id, a.Organization = proto.String("asset-"+req.GetAsset().GetSn()), proto.String(org)
	f.assets[a.GetSn()] = a
	return &connectorpb.ConnectorResponse{Response: &connectorpb.ConnectorResponse_Asset{Asset: a}}, nil
}

func (f *fakeConnector) DescribeAssetClaim(_ context.Context, req *connectorpb.DescribeAssetClaimRequest) (*connectorpb.AssetClaimDescriptionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if org, ok := f.codes[req.GetCode()]; ok {
		return &connectorpb.AssetClaimDescriptionResponse{OrganizationName: proto.String("Org " + org)}, nil
	}
	return &connectorpb.AssetClaimDescriptionResponse{HasErrors: true}, nil
}

func serve(t *testing.T, f *fakeConnector) *ServiceImpl {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	connectorpb.RegisterConnectorServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); srv.Stop() })
	return NewServiceImpl(connectorpb.NewConnectorServiceClient(conn), quiet())
}

func drone(sn string) *domains.AssetDTO {
	return &domains.AssetDTO{SN: proto.String(sn), Name: proto.String("Drone " + sn)}
}

func TestEnsureAssetRedeemsTheClaimOnlyForAnUnknownSerial(t *testing.T) {
	f := &fakeConnector{assets: map[string]*assetpb.AssetProtoDTO{}, codes: map[string]string{"PAIR-1": "org-1"}}
	svc := serve(t, f)
	ctx := context.Background()

	got, err := svc.EnsureAsset(ctx, drone("SN-1"), "PAIR-1")
	if err != nil || got == nil || got.ID == nil || *got.ID != "asset-SN-1" {
		t.Fatalf("first start: asset=%+v err=%v, want the asset the claim created", got, err)
	}
	if len(f.redeemed) != 1 || f.redeemed[0].GetCode() != "PAIR-1" || f.redeemed[0].GetAsset().GetSn() != "SN-1" {
		t.Fatalf("redeemed = %v", f.redeemed)
	}

	again, err := svc.EnsureAsset(ctx, drone("SN-1"), "PAIR-1")
	if err != nil || again == nil || *again.ID != "asset-SN-1" || len(f.redeemed) != 1 {
		t.Fatalf("restart: asset=%+v err=%v redemptions=%d, want the existing asset and no second redemption", again, err, len(f.redeemed))
	}
}

func TestEnsureAssetWithoutACodeCreatesNothing(t *testing.T) {
	f := &fakeConnector{assets: map[string]*assetpb.AssetProtoDTO{}, codes: map[string]string{}}
	got, err := serve(t, f).EnsureAsset(context.Background(), drone("SN-2"), "")
	if err != nil || got != nil || len(f.redeemed) != 0 {
		t.Fatalf("asset=%+v err=%v redemptions=%d, want nothing created", got, err, len(f.redeemed))
	}
}

func TestARefusedClaimIsNilNotAnError(t *testing.T) {
	f := &fakeConnector{assets: map[string]*assetpb.AssetProtoDTO{}, codes: map[string]string{}}
	svc := serve(t, f)
	got, err := svc.RedeemAssetClaim(context.Background(), "WRONG", drone("SN-3"))
	if err != nil || got != nil {
		t.Fatalf("asset=%+v err=%v, want (nil, nil)", got, err)
	}
	if _, err := svc.RedeemAssetClaim(context.Background(), "", drone("SN-3")); err == nil {
		t.Fatal("an empty code must be refused before it reaches connector")
	}
}

func TestDescribeAssetClaimNamesTheOrganizationWithoutSpendingTheCode(t *testing.T) {
	f := &fakeConnector{assets: map[string]*assetpb.AssetProtoDTO{}, codes: map[string]string{"PAIR-1": "org-1"}}
	svc := serve(t, f)
	name, err := svc.DescribeAssetClaim(context.Background(), "PAIR-1")
	if err != nil || name != "Org org-1" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, ok := f.codes["PAIR-1"]; !ok {
		t.Fatal("describing a code must not spend it")
	}
	if name, err := svc.DescribeAssetClaim(context.Background(), "WRONG"); err != nil || name != "" {
		t.Fatalf("unknown code: name=%q err=%v, want (\"\", nil)", name, err)
	}
}
