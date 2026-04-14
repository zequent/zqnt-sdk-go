package missionautonomy

import (
	"context"
	"log/slog"

	"edge-go-sdk/adapter/domains"
	"edge-go-sdk/connector"
	"edge-go-sdk/internal/protohelpers"
	"edge-go-sdk/internal/retry"
	// zqntpbbuf message types (package _go → alias it)
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"

	// gRPC service stubs (package _gogrpc → alias it)
	zqntgrpc "buf.build/gen/go/zqnt/protos/grpc/go/_gogrpc"
)

// ServiceImpl is the gRPC-backed MissionAutonomyService implementation.
type ServiceImpl struct {
	stub   zqntgrpc.MissionAutonomyServiceClient
	mapper *connector.Mapper
	log    *slog.Logger
}

// NewServiceImpl creates a new MissionAutonomyService implementation.
func NewServiceImpl(stub zqntgrpc.MissionAutonomyServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{stub: stub, mapper: &connector.Mapper{}, log: log}
}

func newBase() *zqntpb.RequestBase {
	return &zqntpb.RequestBase{
		Tid:       protohelpers.GenerateTID(),
		Timestamp: protohelpers.Now(),
	}
}

// ---- Mission ----------------------------------------------------------------

func (s *ServiceImpl) CreateMission(ctx context.Context, req *zqntpb.CreateMissionRequest) (*domains.MissionDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.MissionResponse, error) {
		return s.stub.CreateMission(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("CreateMission error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.MissionFromProto(resp.GetMissionDTO()), nil
}

func (s *ServiceImpl) UpdateMission(ctx context.Context, req *zqntpb.UpdateMissionRequest) (*domains.MissionDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.MissionResponse, error) {
		return s.stub.UpdateMission(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("UpdateMission error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.MissionFromProto(resp.GetMissionDTO()), nil
}

func (s *ServiceImpl) GetMission(ctx context.Context, req *zqntpb.GetMissionRequest) (*domains.MissionDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.MissionResponse, error) {
		return s.stub.GetMission(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetMission error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.MissionFromProto(resp.GetMissionDTO()), nil
}

func (s *ServiceImpl) DeleteMission(ctx context.Context, req *zqntpb.DeleteMissionRequest) (bool, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.MissionResponse, error) {
		return s.stub.DeleteMission(c, req)
	})
	if err != nil {
		return false, err
	}
	if resp.GetHasErrors() {
		s.log.Error("DeleteMission error", "error", resp.GetError())
		return false, nil
	}
	return true, nil
}

// ---- Task -------------------------------------------------------------------

func (s *ServiceImpl) GetTask(ctx context.Context, req *zqntpb.GetTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.GetTask(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetTask error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

func (s *ServiceImpl) GetTaskByFlightID(ctx context.Context, req *zqntpb.GetTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.GetTaskByFlightId(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetTaskByFlightID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

func (s *ServiceImpl) CreateTask(ctx context.Context, req *zqntpb.CreateTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.CreateTask(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("CreateTask error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

func (s *ServiceImpl) UpdateTask(ctx context.Context, req *zqntpb.UpdateTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.UpdateTask(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("UpdateTask error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

func (s *ServiceImpl) DeleteTask(ctx context.Context, req *zqntpb.DeleteTaskRequest) (bool, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.DeleteTask(c, req)
	})
	if err != nil {
		return false, err
	}
	if resp.GetHasErrors() {
		s.log.Error("DeleteTask error", "error", resp.GetError())
		return false, nil
	}
	return true, nil
}

func (s *ServiceImpl) StartTask(ctx context.Context, req *zqntpb.StartTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.StartTask(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("StartTask error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

func (s *ServiceImpl) StopTask(ctx context.Context, req *zqntpb.StopTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.TaskResponse, error) {
		return s.stub.StopTask(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("StopTask error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

// ---- Scheduler --------------------------------------------------------------

func (s *ServiceImpl) GetScheduler(ctx context.Context, req *zqntpb.GetSchedulerRequest) (*domains.SchedulerDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.SchedulerResponse, error) {
		return s.stub.GetScheduler(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetScheduler error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.SchedulerFromProto(resp.GetSchedulerDTO()), nil
}

func (s *ServiceImpl) CreateScheduler(ctx context.Context, req *zqntpb.CreateSchedulerRequest) (*domains.SchedulerDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.SchedulerResponse, error) {
		return s.stub.CreateScheduler(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("CreateScheduler error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.SchedulerFromProto(resp.GetSchedulerDTO()), nil
}

func (s *ServiceImpl) UpdateScheduler(ctx context.Context, req *zqntpb.UpdateSchedulerRequest) (*domains.SchedulerDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.SchedulerResponse, error) {
		return s.stub.UpdateScheduler(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("UpdateScheduler error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.SchedulerFromProto(resp.GetSchedulerDTO()), nil
}

func (s *ServiceImpl) DeleteScheduler(ctx context.Context, req *zqntpb.DeleteSchedulerRequest) (bool, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*zqntpb.SchedulerResponse, error) {
		return s.stub.DeleteScheduler(c, req)
	})
	if err != nil {
		return false, err
	}
	if resp.GetHasErrors() {
		s.log.Error("DeleteScheduler error", "error", resp.GetError())
		return false, nil
	}
	return true, nil
}
