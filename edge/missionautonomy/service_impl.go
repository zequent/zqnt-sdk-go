package missionautonomy

import (
	"context"
	"log/slog"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/connector"
	proto "github.com/Zequent/zqnt-edge-sdk-go/gen/proto"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/retry"
)

// ServiceImpl is the gRPC-backed MissionAutonomyService implementation.
type ServiceImpl struct {
	stub   proto.MissionAutonomyServiceClient
	mapper *connector.Mapper
	log    *slog.Logger
}

// NewServiceImpl creates a new MissionAutonomyService implementation.
func NewServiceImpl(stub proto.MissionAutonomyServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{stub: stub, mapper: &connector.Mapper{}, log: log}
}

func newBase() *proto.RequestBase {
	return &proto.RequestBase{
		Tid:       protohelpers.GenerateTID(),
		Timestamp: protohelpers.Now(),
	}
}

// ---- Mission ----------------------------------------------------------------

func (s *ServiceImpl) CreateMission(ctx context.Context, req *proto.CreateMissionRequest) (*domains.MissionDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.MissionResponse, error) {
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

func (s *ServiceImpl) UpdateMission(ctx context.Context, req *proto.UpdateMissionRequest) (*domains.MissionDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.MissionResponse, error) {
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

func (s *ServiceImpl) GetMission(ctx context.Context, req *proto.GetMissionRequest) (*domains.MissionDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.MissionResponse, error) {
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

func (s *ServiceImpl) DeleteMission(ctx context.Context, req *proto.DeleteMissionRequest) (bool, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.MissionResponse, error) {
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

func (s *ServiceImpl) GetTask(ctx context.Context, req *proto.GetTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) GetTaskByFlightID(ctx context.Context, req *proto.GetTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) CreateTask(ctx context.Context, req *proto.CreateTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) UpdateTask(ctx context.Context, req *proto.UpdateTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) DeleteTask(ctx context.Context, req *proto.DeleteTaskRequest) (bool, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) StartTask(ctx context.Context, req *proto.StartTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) StopTask(ctx context.Context, req *proto.StopTaskRequest) (*domains.TaskDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.TaskResponse, error) {
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

func (s *ServiceImpl) GetScheduler(ctx context.Context, req *proto.GetSchedulerRequest) (*domains.SchedulerDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.SchedulerResponse, error) {
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

func (s *ServiceImpl) CreateScheduler(ctx context.Context, req *proto.CreateSchedulerRequest) (*domains.SchedulerDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.SchedulerResponse, error) {
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

func (s *ServiceImpl) UpdateScheduler(ctx context.Context, req *proto.UpdateSchedulerRequest) (*domains.SchedulerDTO, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.SchedulerResponse, error) {
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

func (s *ServiceImpl) DeleteScheduler(ctx context.Context, req *proto.DeleteSchedulerRequest) (bool, error) {
	if req.Base == nil {
		req.Base = newBase()
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.SchedulerResponse, error) {
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
