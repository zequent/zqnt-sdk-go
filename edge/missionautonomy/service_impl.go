package missionautonomy

import (
	"context"
	"log/slog"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/internal/retry"
	commonpb "github.com/zequent/zqnt-utils-golang/gen/common/proto"
	missionautonomycontractspb "github.com/zequent/zqnt-utils-golang/gen/missionautonomy/contracts/proto"
	missionautonomydtopb "github.com/zequent/zqnt-utils-golang/gen/missionautonomy/dto/proto"
	missionautonomypb "github.com/zequent/zqnt-utils-golang/gen/missionautonomy/proto"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// ServiceImpl is the gRPC-backed MissionAutonomyService implementation.
type ServiceImpl struct {
	stub missionautonomypb.MissionAutonomyServiceClient
	log  *slog.Logger
}

// NewServiceImpl creates a new MissionAutonomyService implementation.
func NewServiceImpl(stub missionautonomypb.MissionAutonomyServiceClient, log *slog.Logger) *ServiceImpl {
	return &ServiceImpl{stub: stub, log: log}
}

func newBase(sn string) *commonpb.RequestBase {
	return &commonpb.RequestBase{
		Tid:       protohelpers.GenerateTID(),
		Sn:        sn,
		Timestamp: protohelpers.Now(),
	}
}

// ---- Scheduler ----------------------------------------------------------------

func (s *ServiceImpl) GetScheduler(ctx context.Context, schedulerID string, sn string) (*domains.SchedulerDTO, error) {
	req := &missionautonomycontractspb.GetSchedulerRequest{
		Base:        newBase(sn),
		SchedulerId: &schedulerID,
	}
	resp, err := retry.Do(ctx, func(c context.Context) (*missionautonomycontractspb.SchedulerResponse, error) {
		return s.stub.GetScheduler(c, req)
	})
	if err != nil {
		return nil, err
	}
	if resp.GetHasErrors() {
		s.log.Error("GetScheduler error", "error", resp.GetError())
		return nil, nil
	}
	return schedulerFromProto(resp.GetScheduler()), nil
}

// ---- mapping --------------------------------------------------------------------

func schedulerFromProto(p *missionautonomydtopb.SchedulerProtoDTO) *domains.SchedulerDTO {
	if p == nil {
		return nil
	}
	return &domains.SchedulerDTO{
		ID:             p.Id,
		Name:           p.Name,
		MissionID:      p.MissionId,
		TaskID:         p.TaskId,
		CronExpression: p.CronExpression,
		Type:           p.Type.String(),
		Active:         p.Active,
		ClientTimeZone: p.ClientTimeZone,
		CreatedAt:      tPtr(p.CreatedAt),
		ModifiedAt:     tPtr(p.ModifiedAt),
	}
}

func tPtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
