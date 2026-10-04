// Package missionautonomy provides the MissionAutonomyService interface and its gRPC-backed
// implementation.
//
// Deliberately one method wide. Scheduler lookup is the one thing an edge adapter genuinely needs
// from mission-autonomy (to know its own schedule); reaching through to the service's own
// authoring and execution surface -- Applications, Skills and the SkillExecution lifecycle that
// replaced Mission/Task CRUD on the 2.0.0 contract -- has always been a console/platform-side
// concern, so that migration cost this interface nothing: the reshape landed entirely in
// SchedulerDTO underneath it (see its doc comment).
package missionautonomy

import (
	"context"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
)

// MissionAutonomyService is the client-side interface for the MissionAutonomy backend.
type MissionAutonomyService interface {
	GetScheduler(ctx context.Context, schedulerID string, sn string) (*domains.SchedulerDTO, error)
}
