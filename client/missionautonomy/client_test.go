package missionautonomy

import (
	"context"
	"net"
	"testing"

	execdto "github.com/Zequent/zqnt-client-sdk-go/gen/execution/contracts/proto"
	execution "github.com/Zequent/zqnt-client-sdk-go/gen/execution/dto/proto"
	missionautonomypb "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeService struct {
	missionautonomypb.UnimplementedMissionAutonomyServiceServer
	lastExecuteRequest   *execdto.ExecuteSkillRequest
	lastLifecycleRequest *execdto.SkillExecutionLifecycleRequest
	execution            *execution.SkillExecutionProtoDTO
}

func (s *fakeService) ExecuteSkill(ctx context.Context, req *execdto.ExecuteSkillRequest) (*execdto.SkillExecutionResponse, error) {
	s.lastExecuteRequest = req
	exec := &execution.SkillExecutionProtoDTO{Id: "exec-1", AssetSn: req.GetBase().GetSn()}
	return &execdto.SkillExecutionResponse{Response: &execdto.SkillExecutionResponse_Execution{Execution: exec}}, nil
}

func (s *fakeService) StartSkillExecution(ctx context.Context, req *execdto.SkillExecutionLifecycleRequest) (*execdto.SkillExecutionResponse, error) {
	s.lastLifecycleRequest = req
	return &execdto.SkillExecutionResponse{
		Response: &execdto.SkillExecutionResponse_Execution{Execution: &execution.SkillExecutionProtoDTO{Id: req.GetExecutionId()}},
	}, nil
}

func (s *fakeService) UpsertApplication(ctx context.Context, req *execdto.UpsertApplicationRequest) (*execdto.ApplicationResponse, error) {
	return &execdto.ApplicationResponse{Response: &execdto.ApplicationResponse_Application{Application: req.GetApplication()}}, nil
}

func dialFake(t *testing.T, svc missionautonomypb.MissionAutonomyServiceServer) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	missionautonomypb.RegisterMissionAutonomyServiceServer(server, svc)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestExecuteSimpleSendsASimpleSpecAndCarriesTheAssetSnThrough(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.ExecuteSimple(context.Background(), "asset-1", "flight.takeoff", nil, "idem-1")
	if err != nil {
		t.Fatalf("ExecuteSimple: %v", err)
	}
	if got.GetId() != "exec-1" {
		t.Fatalf("expected exec-1, got %q", got.GetId())
	}
	simple := fake.lastExecuteRequest.GetSpec().GetSimple()
	if simple == nil || simple.GetCommandId() != "flight.takeoff" {
		t.Fatalf("expected a simple spec for flight.takeoff, got %v", fake.lastExecuteRequest.GetSpec())
	}
	if fake.lastExecuteRequest.GetBase().GetSn() != "asset-1" {
		t.Fatalf("expected the asset sn to be sent, got %q", fake.lastExecuteRequest.GetBase().GetSn())
	}
}

func TestStartSkillExecutionSendsTheExecutionId(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.StartSkillExecution(context.Background(), "exec-42")
	if err != nil {
		t.Fatalf("StartSkillExecution: %v", err)
	}
	if got.GetId() != "exec-42" {
		t.Fatalf("expected exec-42, got %q", got.GetId())
	}
	if fake.lastLifecycleRequest.GetExecutionId() != "exec-42" {
		t.Fatalf("expected the execution id to be sent, got %q", fake.lastLifecycleRequest.GetExecutionId())
	}
}

func TestUpsertApplicationRoundTripsTheApplication(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.UpsertApplication(context.Background(), &execution.ApplicationProtoDTO{Id: "app-1", Version: "1"}, "")
	if err != nil {
		t.Fatalf("UpsertApplication: %v", err)
	}
	if got.GetId() != "app-1" {
		t.Fatalf("expected app-1, got %q", got.GetId())
	}
}
