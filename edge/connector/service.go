// Package connector provides the ConnectorService interface and its gRPC-backed
// implementation for managing assets, missions, tasks, schedulers, and organizations.
package connector

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// ConnectorService is the client-side interface for the Connector backend service.
// All methods return (result, error); callers wrap in goroutines for concurrency.
type ConnectorService interface {
	// Asset operations
	GetAssetBySN(ctx context.Context, sn string) (*domains.AssetDTO, error)
	GetAssetByID(ctx context.Context, id string) (*domains.AssetDTO, error)
	GetSubAssetBySN(ctx context.Context, sn string) (*domains.SubAssetDTO, error)
	UpdateAsset(ctx context.Context, id string, asset *domains.AssetDTO) (*domains.AssetDTO, error)
	RegisterAsset(ctx context.Context, asset *domains.AssetDTO) (*domains.AssetDTO, error)
	DeRegisterAsset(ctx context.Context, id string) (bool, error)

	// Mission operations
	GetMissionByID(ctx context.Context, id string) (*domains.MissionDTO, error)
	CreateMission(ctx context.Context, mission *domains.MissionDTO) (*domains.MissionDTO, error)
	UpdateMission(ctx context.Context, id string, mission *domains.MissionDTO) (*domains.MissionDTO, error)
	DeleteMission(ctx context.Context, id string) (bool, error)

	// Task operations
	GetTaskByID(ctx context.Context, id string) (*domains.TaskDTO, error)
	GetTaskByFlightID(ctx context.Context, flightID string) (*domains.TaskDTO, error)
	CreateTask(ctx context.Context, task *domains.TaskDTO) (*domains.TaskDTO, error)
	UpdateTask(ctx context.Context, id string, task *domains.TaskDTO) (*domains.TaskDTO, error)
	DeleteTask(ctx context.Context, id string) (bool, error)

	// Scheduler operations
	GetSchedulerByID(ctx context.Context, id string) (*domains.SchedulerDTO, error)
	CreateScheduler(ctx context.Context, s *domains.SchedulerDTO) (*domains.SchedulerDTO, error)
	UpdateScheduler(ctx context.Context, id string, s *domains.SchedulerDTO) (*domains.SchedulerDTO, error)
	DeleteScheduler(ctx context.Context, id string) (bool, error)

	// Organization operations
	GetOrganizationByID(ctx context.Context, id string) (*domains.OrganizationDTO, error)
}
