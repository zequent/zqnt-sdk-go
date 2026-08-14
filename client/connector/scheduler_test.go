package connector

import (
	"context"
	"testing"

	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
	schedulerpb "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/contracts/proto"
	schedulerdto "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/dto/proto"
)

type fakeSchedulerService struct {
	connectorpb.UnimplementedConnectorServiceServer
	lastCreate *schedulerpb.CreateSchedulerRequest
	lastDelete *schedulerpb.DeleteSchedulerRequest
}

func (s *fakeSchedulerService) CreateScheduler(ctx context.Context, req *schedulerpb.CreateSchedulerRequest) (*schedulerpb.SchedulerResponse, error) {
	s.lastCreate = req
	return &schedulerpb.SchedulerResponse{Response: &schedulerpb.SchedulerResponse_Scheduler{Scheduler: req.GetScheduler()}}, nil
}

func (s *fakeSchedulerService) GetScheduler(ctx context.Context, req *schedulerpb.GetSchedulerRequest) (*schedulerpb.SchedulerResponse, error) {
	return &schedulerpb.SchedulerResponse{
		Response: &schedulerpb.SchedulerResponse_Scheduler{Scheduler: &schedulerdto.SchedulerProtoDTO{Id: req.SchedulerId, Name: "nightly-scan"}},
	}, nil
}

func (s *fakeSchedulerService) DeleteScheduler(ctx context.Context, req *schedulerpb.DeleteSchedulerRequest) (*schedulerpb.SchedulerResponse, error) {
	s.lastDelete = req
	return &schedulerpb.SchedulerResponse{Response: &schedulerpb.SchedulerResponse_Empty{}}, nil
}

func TestCreateSchedulerRoundTripsTheScheduler(t *testing.T) {
	fake := &fakeSchedulerService{}
	client := dialFake(t, fake)

	got, err := client.CreateScheduler(context.Background(), &schedulerdto.SchedulerProtoDTO{Name: "hourly-patrol", CronExpression: "0 * * * *"})
	if err != nil {
		t.Fatalf("CreateScheduler: %v", err)
	}
	if got.GetName() != "hourly-patrol" {
		t.Fatalf("expected hourly-patrol, got %q", got.GetName())
	}
	if fake.lastCreate.GetScheduler().GetCronExpression() != "0 * * * *" {
		t.Fatalf("expected the cron expression to be sent, got %v", fake.lastCreate)
	}
}

func TestGetSchedulerReturnsTheScheduler(t *testing.T) {
	fake := &fakeSchedulerService{}
	client := dialFake(t, fake)

	got, err := client.GetScheduler(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("GetScheduler: %v", err)
	}
	if got.GetId() != "sched-1" {
		t.Fatalf("expected sched-1, got %q", got.GetId())
	}
}

func TestDeleteSchedulerSendsTheId(t *testing.T) {
	fake := &fakeSchedulerService{}
	client := dialFake(t, fake)

	if err := client.DeleteScheduler(context.Background(), "sched-9"); err != nil {
		t.Fatalf("DeleteScheduler: %v", err)
	}
	if fake.lastDelete.GetSchedulerId() != "sched-9" {
		t.Fatalf("expected sched-9 to be sent, got %q", fake.lastDelete.GetSchedulerId())
	}
}
