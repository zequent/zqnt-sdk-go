package connector

import (
	"context"
	"net"
	"testing"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeConnectorService struct {
	connectorpb.UnimplementedConnectorServiceServer
	lastObserved       *connectorpb.SkillContractProtoDTO
	lastListRequest    *connectorpb.ListSkillContractsRequest
	lastPermissionsReq *connectorpb.SetSkillContractPermissionsRequest
}

func (s *fakeConnectorService) ObserveSkillContract(ctx context.Context, req *connectorpb.UpsertSkillContractRequest) (*connectorpb.SkillContractResponse, error) {
	s.lastObserved = req.GetContract()
	return &connectorpb.SkillContractResponse{Contract: req.GetContract()}, nil
}

func (s *fakeConnectorService) ListSkillContracts(ctx context.Context, req *connectorpb.ListSkillContractsRequest) (*connectorpb.SkillContractListResponse, error) {
	s.lastListRequest = req
	return &connectorpb.SkillContractListResponse{
		Contracts: []*connectorpb.SkillContractProtoDTO{{CommandId: "flight.takeoff"}},
	}, nil
}

func (s *fakeConnectorService) SetSkillContractPermissions(ctx context.Context, req *connectorpb.SetSkillContractPermissionsRequest) (*connectorpb.SkillContractResponse, error) {
	s.lastPermissionsReq = req
	return &connectorpb.SkillContractResponse{
		Contract: &connectorpb.SkillContractProtoDTO{Id: req.GetId(), RequiredPermissions: req.GetRequiredPermissions()},
	}, nil
}

func dialFake(t *testing.T, svc connectorpb.ConnectorServiceServer) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	connectorpb.RegisterConnectorServiceServer(server, svc)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestObserveSkillContractRoundTripsTheContract(t *testing.T) {
	fake := &fakeConnectorService{}
	client := dialFake(t, fake)

	got, err := client.ObserveSkillContract(context.Background(), &connectorpb.SkillContractProtoDTO{CommandId: "acme.custom_scan"})
	if err != nil {
		t.Fatalf("ObserveSkillContract: %v", err)
	}
	if got.GetCommandId() != "acme.custom_scan" {
		t.Fatalf("expected commandId to round-trip, got %q", got.GetCommandId())
	}
	if fake.lastObserved.GetCommandId() != "acme.custom_scan" {
		t.Fatalf("expected the server to receive the contract, got %v", fake.lastObserved)
	}
}

func TestListSkillContractsFiltersByCommandId(t *testing.T) {
	fake := &fakeConnectorService{}
	client := dialFake(t, fake)

	got, err := client.ListSkillContracts(context.Background(), nil, "flight.takeoff")
	if err != nil {
		t.Fatalf("ListSkillContracts: %v", err)
	}
	if len(got) != 1 || got[0].GetCommandId() != "flight.takeoff" {
		t.Fatalf("expected one flight.takeoff contract, got %v", got)
	}
	if fake.lastListRequest.GetCommandId() != "flight.takeoff" {
		t.Fatalf("expected commandId to be sent, got %q", fake.lastListRequest.GetCommandId())
	}
}

func TestSetSkillContractPermissionsIsAFullReplacement(t *testing.T) {
	fake := &fakeConnectorService{}
	client := dialFake(t, fake)

	got, err := client.SetSkillContractPermissions(context.Background(), "row-1", []string{"mission.launch", "role:pilot"})
	if err != nil {
		t.Fatalf("SetSkillContractPermissions: %v", err)
	}
	if len(got.GetRequiredPermissions()) != 2 {
		t.Fatalf("expected both permissions to round-trip, got %v", got.GetRequiredPermissions())
	}
}

func TestGetAssetBySnReturnsAnErrorOnAServerReportedFailure(t *testing.T) {
	fake := &erroringConnectorService{message: "asset not found"}
	client := dialFake(t, fake)

	_, err := client.GetAssetBySn(context.Background(), "unknown")
	if err == nil {
		t.Fatalf("expected an error, got none")
	}
}

type erroringConnectorService struct {
	connectorpb.UnimplementedConnectorServiceServer
	message string
}

func (s *erroringConnectorService) GetAssetBySn(ctx context.Context, req *base.RequestBase) (*connectorpb.ConnectorResponse, error) {
	hasErrors := true
	return &connectorpb.ConnectorResponse{HasErrors: &hasErrors,
		Response: &connectorpb.ConnectorResponse_Error{Error: &base.GlobalErrorMessage{ErrorMessage: s.message}}}, nil
}
