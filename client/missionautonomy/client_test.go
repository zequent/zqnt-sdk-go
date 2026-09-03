package missionautonomy

import (
	"context"
	"net"
	"testing"

	missionautonomycontracts "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/contracts/proto"
	missionautonomydto "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/dto/proto"
	missionautonomypb "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	protobuf "google.golang.org/protobuf/proto"
)

type fakeService struct {
	missionautonomypb.UnimplementedMissionAutonomyServiceServer
	lastCreateMission  *missionautonomycontracts.CreateMissionRequest
	lastCreateTask     *missionautonomycontracts.CreateTaskRequest
	lastLifecycle      *missionautonomycontracts.TaskLifecycleRequest
	lastListSchedulers *missionautonomycontracts.ListSchedulersRequest
}

func (s *fakeService) CreateMission(ctx context.Context, req *missionautonomycontracts.CreateMissionRequest) (*missionautonomycontracts.MissionResponse, error) {
	s.lastCreateMission = req
	m := &missionautonomydto.MissionProtoDTO{Id: protobuf.String("m-1"), Name: req.GetMission().GetName()}
	return &missionautonomycontracts.MissionResponse{Response: &missionautonomycontracts.MissionResponse_Mission{Mission: m}}, nil
}

func (s *fakeService) GetMission(ctx context.Context, req *missionautonomycontracts.GetMissionRequest) (*missionautonomycontracts.MissionResponse, error) {
	m := &missionautonomydto.MissionProtoDTO{Id: protobuf.String(req.GetMissionId()), Name: "Perimeter sweep"}
	return &missionautonomycontracts.MissionResponse{Response: &missionautonomycontracts.MissionResponse_Mission{Mission: m}}, nil
}

func (s *fakeService) CreateTask(ctx context.Context, req *missionautonomycontracts.CreateTaskRequest) (*missionautonomycontracts.TaskResponse, error) {
	s.lastCreateTask = req
	t := &missionautonomydto.TaskProtoDTO{Id: protobuf.String("t-1"), Name: protobuf.String(req.GetTask().GetName())}
	return &missionautonomycontracts.TaskResponse{Response: &missionautonomycontracts.TaskResponse_Task{Task: t}}, nil
}

func (s *fakeService) GetTaskByFlightId(ctx context.Context, req *missionautonomycontracts.GetTaskByFlightIdRequest) (*missionautonomycontracts.TaskResponse, error) {
	t := &missionautonomydto.TaskProtoDTO{Id: protobuf.String("t-2"), ExternalTaskId: protobuf.String(req.GetFlightId())}
	return &missionautonomycontracts.TaskResponse{Response: &missionautonomycontracts.TaskResponse_Task{Task: t}}, nil
}

func (s *fakeService) StartTask(ctx context.Context, req *missionautonomycontracts.TaskLifecycleRequest) (*missionautonomycontracts.TaskResponse, error) {
	s.lastLifecycle = req
	t := &missionautonomydto.TaskProtoDTO{Id: protobuf.String(req.GetTaskId())}
	return &missionautonomycontracts.TaskResponse{Response: &missionautonomycontracts.TaskResponse_Task{Task: t}}, nil
}

func (s *fakeService) DeleteMission(ctx context.Context, req *missionautonomycontracts.DeleteMissionRequest) (*missionautonomycontracts.MissionResponse, error) {
	return &missionautonomycontracts.MissionResponse{Response: &missionautonomycontracts.MissionResponse_Empty{}}, nil
}

func (s *fakeService) ListSchedulers(ctx context.Context, req *missionautonomycontracts.ListSchedulersRequest) (*missionautonomycontracts.SchedulerResponse, error) {
	s.lastListSchedulers = req
	list := &missionautonomydto.SchedulerProtoDTOList{
		SchedulerDtoList: []*missionautonomydto.SchedulerProtoDTO{{Id: protobuf.String("s-1"), Name: "daily"}},
	}
	return &missionautonomycontracts.SchedulerResponse{Response: &missionautonomycontracts.SchedulerResponse_Schedulers{Schedulers: list}}, nil
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

func TestCreateMissionRoundTripsTheMission(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.CreateMission(context.Background(), &missionautonomydto.MissionProtoDTO{Name: "Perimeter sweep"})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if got.GetName() != "Perimeter sweep" {
		t.Fatalf("expected Perimeter sweep, got %q", got.GetName())
	}
	if fake.lastCreateMission.GetMission().GetName() != "Perimeter sweep" {
		t.Fatalf("expected the mission to be sent, got %v", fake.lastCreateMission)
	}
}

func TestGetMissionReturnsTheMission(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.GetMission(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("GetMission: %v", err)
	}
	if got.GetId() != "m-1" {
		t.Fatalf("expected m-1, got %q", got.GetId())
	}
}

func TestDeleteMissionSucceeds(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	if err := client.DeleteMission(context.Background(), "m-1"); err != nil {
		t.Fatalf("DeleteMission: %v", err)
	}
}

func TestCreateTaskRoundTripsTheTask(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.CreateTask(context.Background(), &missionautonomydto.TaskProtoDTO{Name: protobuf.String("Waypoint run")})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if got.GetName() != "Waypoint run" {
		t.Fatalf("expected Waypoint run, got %q", got.GetName())
	}
	if fake.lastCreateTask.GetTask().GetName() != "Waypoint run" {
		t.Fatalf("expected the task to be sent, got %v", fake.lastCreateTask)
	}
}

func TestGetTaskByFlightIdSendsTheFlightId(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.GetTaskByFlightID(context.Background(), "flight-42")
	if err != nil {
		t.Fatalf("GetTaskByFlightID: %v", err)
	}
	if got.GetExternalTaskId() != "flight-42" {
		t.Fatalf("expected flight-42, got %q", got.GetExternalTaskId())
	}
}

func TestListSchedulersFiltersByTaskId(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.ListSchedulers(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("ListSchedulers: %v", err)
	}
	if len(got) != 1 || got[0].GetId() != "s-1" {
		t.Fatalf("expected one s-1 scheduler, got %v", got)
	}
	if fake.lastListSchedulers.GetTaskId() != "t-1" {
		t.Fatalf("expected the task id to be sent, got %q", fake.lastListSchedulers.GetTaskId())
	}
}

func TestStartTaskSendsTheTaskId(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	got, err := client.StartTask(context.Background(), "t-42")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if got.GetId() != "t-42" {
		t.Fatalf("expected t-42, got %q", got.GetId())
	}
	if fake.lastLifecycle.GetTaskId() != "t-42" {
		t.Fatalf("expected the task id to be sent, got %q", fake.lastLifecycle.GetTaskId())
	}
}
