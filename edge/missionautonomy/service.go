// Package missionautonomy provides the MissionAutonomyService interface and its gRPC-backed
// implementation.
//
// Reshaped 2026-09-02: Mission/Task CRUD (CreateMission/UpdateMission/GetTask/StartTask/...) was
// retired from MissionAutonomyService entirely in the platform's Skill/Application migration --
// those RPCs no longer exist on the current mission-autonomy.proto at all, replaced by the
// capability-execution model (Application/SkillExecution). The edge-side surface is deliberately
// tiny as a result: Application/SkillExecution administration is a console/platform-side concern,
// not something an edge adapter itself calls. Mirrors edge-python-sdk's own already-migrated
// MissionAutonomyClient (edge_sdk/client/mission_autonomy_client.py) exactly -- Scheduler lookup
// is the only thing that survived on the edge side.
package missionautonomy

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// MissionAutonomyService is the client-side interface for the MissionAutonomy backend.
type MissionAutonomyService interface {
	GetScheduler(ctx context.Context, schedulerID string, sn string) (*domains.SchedulerDTO, error)
}
