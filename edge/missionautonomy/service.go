// Package missionautonomy provides the MissionAutonomyService interface and its gRPC-backed
// implementation.
//
// v1.3.0 wire shape (this branch depends on zqnt-utils-golang v1.3.0, not the current-schema
// proto): Mission/Task CRUD and Task-lifecycle RPCs (CreateMission/GetTask/StartTask/...) still
// exist on mission-autonomy.proto at this pin, but nothing here calls them -- an edge adapter has
// never needed to reach through to Mission/Task CRUD itself (that's always been a console/
// platform-side concern), so this interface was already this narrow before the later
// capability-execution migration reshaped the DTOs underneath it. Scheduler lookup is the one
// thing an adapter genuinely needs (to know its own schedule), hence the only method kept.
package missionautonomy

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
)

// MissionAutonomyService is the client-side interface for the MissionAutonomy backend.
type MissionAutonomyService interface {
	GetScheduler(ctx context.Context, schedulerID string, sn string) (*domains.SchedulerDTO, error)
}
