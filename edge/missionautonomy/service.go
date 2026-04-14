// Package missionautonomy provides the MissionAutonomyService interface and its
// gRPC-backed implementation for mission and task execution.
package missionautonomy

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	// zqntpbbuf message types (package _go → alias it)
	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"
)

// MissionAutonomyService is the client-side interface for the MissionAutonomy backend.
type MissionAutonomyService interface {
	CreateMission(ctx context.Context, req *zqntpb.CreateMissionRequest) (*domains.MissionDTO, error)
	UpdateMission(ctx context.Context, req *zqntpb.UpdateMissionRequest) (*domains.MissionDTO, error)
	GetMission(ctx context.Context, req *zqntpb.GetMissionRequest) (*domains.MissionDTO, error)
	DeleteMission(ctx context.Context, req *zqntpb.DeleteMissionRequest) (bool, error)

	GetTask(ctx context.Context, req *zqntpb.GetTaskRequest) (*domains.TaskDTO, error)
	GetTaskByFlightID(ctx context.Context, req *zqntpb.GetTaskRequest) (*domains.TaskDTO, error)
	CreateTask(ctx context.Context, req *zqntpb.CreateTaskRequest) (*domains.TaskDTO, error)
	UpdateTask(ctx context.Context, req *zqntpb.UpdateTaskRequest) (*domains.TaskDTO, error)
	DeleteTask(ctx context.Context, req *zqntpb.DeleteTaskRequest) (bool, error)

	GetScheduler(ctx context.Context, req *zqntpb.GetSchedulerRequest) (*domains.SchedulerDTO, error)
	CreateScheduler(ctx context.Context, req *zqntpb.CreateSchedulerRequest) (*domains.SchedulerDTO, error)
	UpdateScheduler(ctx context.Context, req *zqntpb.UpdateSchedulerRequest) (*domains.SchedulerDTO, error)
	DeleteScheduler(ctx context.Context, req *zqntpb.DeleteSchedulerRequest) (bool, error)

	StartTask(ctx context.Context, req *zqntpb.StartTaskRequest) (*domains.TaskDTO, error)
	StopTask(ctx context.Context, req *zqntpb.StopTaskRequest) (*domains.TaskDTO, error)
}
