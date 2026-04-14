// Package missionautonomy provides the MissionAutonomyService interface and its
// gRPC-backed implementation for mission and task execution.
package missionautonomy

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	proto "github.com/Zequent/zqnt-edge-sdk-go/gen/proto"
)

// MissionAutonomyService is the client-side interface for the MissionAutonomy backend.
type MissionAutonomyService interface {
	CreateMission(ctx context.Context, req *proto.CreateMissionRequest) (*domains.MissionDTO, error)
	UpdateMission(ctx context.Context, req *proto.UpdateMissionRequest) (*domains.MissionDTO, error)
	GetMission(ctx context.Context, req *proto.GetMissionRequest) (*domains.MissionDTO, error)
	DeleteMission(ctx context.Context, req *proto.DeleteMissionRequest) (bool, error)

	GetTask(ctx context.Context, req *proto.GetTaskRequest) (*domains.TaskDTO, error)
	GetTaskByFlightID(ctx context.Context, req *proto.GetTaskRequest) (*domains.TaskDTO, error)
	CreateTask(ctx context.Context, req *proto.CreateTaskRequest) (*domains.TaskDTO, error)
	UpdateTask(ctx context.Context, req *proto.UpdateTaskRequest) (*domains.TaskDTO, error)
	DeleteTask(ctx context.Context, req *proto.DeleteTaskRequest) (bool, error)

	GetScheduler(ctx context.Context, req *proto.GetSchedulerRequest) (*domains.SchedulerDTO, error)
	CreateScheduler(ctx context.Context, req *proto.CreateSchedulerRequest) (*domains.SchedulerDTO, error)
	UpdateScheduler(ctx context.Context, req *proto.UpdateSchedulerRequest) (*domains.SchedulerDTO, error)
	DeleteScheduler(ctx context.Context, req *proto.DeleteSchedulerRequest) (bool, error)

	StartTask(ctx context.Context, req *proto.StartTaskRequest) (*domains.TaskDTO, error)
	StopTask(ctx context.Context, req *proto.StopTaskRequest) (*domains.TaskDTO, error)
}
