package skillregistry

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	connectorpb "github.com/Zequent/zqnt-edge-sdk-go/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeConnectorService struct {
	connectorpb.UnimplementedConnectorServiceServer
	lastObserved    *connectorpb.SkillContractProtoDTO
	lastListRequest *connectorpb.ListSkillContractsRequest
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

func dialFake(t *testing.T, svc connectorpb.ConnectorServiceServer) *ServiceImpl {
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
	return NewServiceImpl(connectorpb.NewConnectorServiceClient(conn), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestObserveSkillContractRoundTripsTheContract(t *testing.T) {
	fake := &fakeConnectorService{}
	svc := dialFake(t, fake)

	got, err := svc.ObserveSkillContract(context.Background(), &connectorpb.SkillContractProtoDTO{CommandId: "acme.custom_scan"})
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
	svc := dialFake(t, fake)

	got, err := svc.ListSkillContracts(context.Background(), nil, "flight.takeoff")
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
