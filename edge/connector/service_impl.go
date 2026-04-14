package connector

import (
	"context"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	proto "github.com/Zequent/zqnt-edge-sdk-go/gen/proto"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/retry"
	"log/slog"
)

// ServiceImpl is the gRPC-backed ConnectorService implementation.
type ServiceImpl struct {
	stub   proto.ConnectorServiceClient
	mapper *Mapper
	log    *slog.Logger
}

// NewServiceImpl creates a new ConnectorService implementation.
func NewServiceImpl(stub proto.ConnectorServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{stub: stub, mapper: &Mapper{}, log: log}
}

// ---- helpers ----------------------------------------------------------------

func newBase(sn string) *proto.RequestBase {
	return &proto.RequestBase{
		Tid:       protohelpers.GenerateTID(),
		Sn:        sn,
		Timestamp: protohelpers.Now(),
	}
}

// ---- Asset operations -------------------------------------------------------

func (s *ServiceImpl) GetAssetBySN(ctx context.Context, sn string) (*domains.AssetDTO, error) {
	req := &proto.ConnectorGetAssetBySnRequest{Base: newBase(sn)}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetAssetBySn(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetAssetBySN error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAssetDTO()), nil
}

func (s *ServiceImpl) GetAssetByID(ctx context.Context, id string) (*domains.AssetDTO, error) {
	req := &proto.ConnectorGetAssetByIdRequest{Base: newBase(""), Id: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetAssetById(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetAssetByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAssetDTO()), nil
}

func (s *ServiceImpl) GetSubAssetBySN(ctx context.Context, sn string) (*domains.SubAssetDTO, error) {
	req := &proto.ConnectorGetSubAssetBySnRequest{Base: newBase(sn)}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetSubAssetBySn(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetSubAssetBySN error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.SubAssetFromProto(resp.GetSubAssetDTO()), nil
}

func (s *ServiceImpl) UpdateAsset(ctx context.Context, id string, asset *domains.AssetDTO) (*domains.AssetDTO, error) {
	req := &proto.ConnectorUpdateAssetRequest{
		Base:     newBase(asset.SN),
		AssetId:  id,
		AssetDTO: s.mapper.AssetToProto(asset),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.UpdateAsset(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("UpdateAsset error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAssetDTO()), nil
}

func (s *ServiceImpl) RegisterAsset(ctx context.Context, asset *domains.AssetDTO) (*domains.AssetDTO, error) {
	req := &proto.ConnectorRegisterAssetRequest{
		Base:     newBase(asset.SN),
		AssetDTO: s.mapper.AssetToProto(asset),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.RegisterAsset(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("RegisterAsset error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.AssetFromProto(resp.GetAssetDTO()), nil
}

func (s *ServiceImpl) DeRegisterAsset(ctx context.Context, _ string) (bool, error) {
	req := &proto.ConnectorDeRegisterAssetRequest{Base: newBase("")}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.DeRegisterAsset(c, req)
	})
	if err != nil {
		return false, err
	}
	if resp.GetHasErrors() {
		s.log.Error("DeRegisterAsset error", "error", resp.GetError())
		return false, nil
	}
	return true, nil
}

// ---- Mission operations -----------------------------------------------------

func (s *ServiceImpl) GetMissionByID(ctx context.Context, id string) (*domains.MissionDTO, error) {
	req := &proto.ConnectorGetMissionRequest{Base: newBase(""), MissionId: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetMission(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetMissionByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.MissionFromProto(resp.GetMissionDTO()), nil
}

func (s *ServiceImpl) CreateMission(ctx context.Context, mission *domains.MissionDTO) (*domains.MissionDTO, error) {
	req := &proto.ConnectorCreateMissionRequest{
		Base:       newBase(""),
		MissionDTO: s.mapper.MissionToProto(mission),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) UpdateMission(ctx context.Context, id string, mission *domains.MissionDTO) (*domains.MissionDTO, error) {
	req := &proto.ConnectorUpdateMissionRequest{
		Base:       newBase(""),
		MissionId:  id,
		MissionDTO: s.mapper.MissionToProto(mission),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) DeleteMission(ctx context.Context, id string) (bool, error) {
	req := &proto.ConnectorDeleteMissionRequest{Base: newBase(""), MissionId: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

// ---- Task operations --------------------------------------------------------

func (s *ServiceImpl) GetTaskByID(ctx context.Context, id string) (*domains.TaskDTO, error) {
	req := &proto.ConnectorGetTaskRequest{Base: newBase(""), TaskId: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetTask(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetTaskByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.TaskFromProto(resp.GetTaskDTO()), nil
}

func (s *ServiceImpl) GetTaskByFlightID(ctx context.Context, flightID string) (*domains.TaskDTO, error) {
	req := &proto.ConnectorGetTaskRequest{Base: newBase(""), TaskId: flightID}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) CreateTask(ctx context.Context, task *domains.TaskDTO) (*domains.TaskDTO, error) {
	req := &proto.ConnectorCreateTaskRequest{
		Base:    newBase(""),
		TaskDTO: s.mapper.TaskToProto(task),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) UpdateTask(ctx context.Context, id string, task *domains.TaskDTO) (*domains.TaskDTO, error) {
	req := &proto.ConnectorUpdateTaskRequest{
		Base:    newBase(""),
		TaskId:  id,
		TaskDTO: s.mapper.TaskToProto(task),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) DeleteTask(ctx context.Context, id string) (bool, error) {
	req := &proto.ConnectorDeleteTaskRequest{Base: newBase(""), TaskId: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

// ---- Scheduler operations ---------------------------------------------------

func (s *ServiceImpl) GetSchedulerByID(ctx context.Context, id string) (*domains.SchedulerDTO, error) {
	req := &proto.ConnectorGetSchedulerRequest{Base: newBase(""), SchedulerId: &id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetScheduler(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetSchedulerByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.SchedulerFromProto(resp.GetSchedulerDTO()), nil
}

func (s *ServiceImpl) CreateScheduler(ctx context.Context, sched *domains.SchedulerDTO) (*domains.SchedulerDTO, error) {
	req := &proto.ConnectorCreateSchedulerRequest{
		Base:         newBase(""),
		SchedulerDTO: s.mapper.SchedulerToProto(sched),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) UpdateScheduler(ctx context.Context, id string, sched *domains.SchedulerDTO) (*domains.SchedulerDTO, error) {
	req := &proto.ConnectorUpdateSchedulerRequest{
		Base:         newBase(""),
		SchedulerId:  id,
		SchedulerDTO: s.mapper.SchedulerToProto(sched),
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

func (s *ServiceImpl) DeleteScheduler(ctx context.Context, id string) (bool, error) {
	req := &proto.ConnectorDeleteSchedulerRequest{Base: newBase(""), SchedulerId: id}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
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

// ---- Organization -----------------------------------------------------------

func (s *ServiceImpl) GetOrganizationByID(ctx context.Context, _ string) (*domains.OrganizationDTO, error) {
	req := &proto.ConnectorGetOrganizationRequest{Base: newBase("")}
	resp, err := retry.Do(ctx, func(c context.Context) (*proto.ConnectorResponse, error) {
		return s.stub.GetOrganization(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetOrganizationByID error", "error", resp.GetError())
		return nil, nil
	}
	return s.mapper.OrgFromProto(resp.GetOrganizationDTO()), nil
}
