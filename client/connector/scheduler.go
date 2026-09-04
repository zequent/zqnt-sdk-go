package connector

import (
	"context"
	"fmt"

	schedulerpb "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/contracts/proto"
	schedulerdto "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/dto/proto"
)

// GetScheduler fetches a scheduler by ID.
func (c *Client) GetScheduler(ctx context.Context, schedulerID string) (*schedulerdto.SchedulerProtoDTO, error) {
	resp, err := c.grpc.GetScheduler(ctx, &schedulerpb.GetSchedulerRequest{Base: requestBase(), SchedulerId: &schedulerID})
	if err != nil {
		return nil, fmt.Errorf("connector: GetScheduler(%s): %w", schedulerID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: GetScheduler(%s): %s", schedulerID, resp.GetError().GetErrorMessage())
	}
	return resp.GetScheduler(), nil
}

// ListSchedulers isn't here on this branch -- ConnectorService has no ListSchedulers RPC at the
// 1.3.0 wire contract this branch tracks (verified against zqnt-protos' own 1.3.0 tag); it only
// exists on MissionAutonomyService -- see missionautonomy.Client.ListSchedulers.

// CreateScheduler creates one scheduler.
func (c *Client) CreateScheduler(ctx context.Context, scheduler *schedulerdto.SchedulerProtoDTO) (*schedulerdto.SchedulerProtoDTO, error) {
	resp, err := c.grpc.CreateScheduler(ctx, &schedulerpb.CreateSchedulerRequest{Base: requestBase(), Scheduler: scheduler})
	if err != nil {
		return nil, fmt.Errorf("connector: CreateScheduler(%s): %w", scheduler.GetName(), err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: CreateScheduler(%s): %s", scheduler.GetName(), resp.GetError().GetErrorMessage())
	}
	return resp.GetScheduler(), nil
}

// CreateSchedulers creates several schedulers in one call.
func (c *Client) CreateSchedulers(ctx context.Context, schedulers []*schedulerdto.SchedulerProtoDTO) ([]*schedulerdto.SchedulerProtoDTO, error) {
	resp, err := c.grpc.CreateSchedulers(ctx, &schedulerpb.CreateSchedulersRequest{Base: requestBase(), Schedulers: schedulers})
	if err != nil {
		return nil, fmt.Errorf("connector: CreateSchedulers: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: CreateSchedulers: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetSchedulers().GetSchedulerDtoList(), nil
}

// UpdateScheduler updates one scheduler by ID.
func (c *Client) UpdateScheduler(ctx context.Context, schedulerID string, scheduler *schedulerdto.SchedulerProtoDTO) (*schedulerdto.SchedulerProtoDTO, error) {
	resp, err := c.grpc.UpdateScheduler(ctx, &schedulerpb.UpdateSchedulerRequest{Base: requestBase(), SchedulerId: schedulerID, Scheduler: scheduler})
	if err != nil {
		return nil, fmt.Errorf("connector: UpdateScheduler(%s): %w", schedulerID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: UpdateScheduler(%s): %s", schedulerID, resp.GetError().GetErrorMessage())
	}
	return resp.GetScheduler(), nil
}

// DeleteScheduler deletes one scheduler by ID.
func (c *Client) DeleteScheduler(ctx context.Context, schedulerID string) error {
	resp, err := c.grpc.DeleteScheduler(ctx, &schedulerpb.DeleteSchedulerRequest{Base: requestBase(), SchedulerId: schedulerID})
	if err != nil {
		return fmt.Errorf("connector: DeleteScheduler(%s): %w", schedulerID, err)
	}
	if resp.GetHasErrors() {
		return fmt.Errorf("connector: DeleteScheduler(%s): %s", schedulerID, resp.GetError().GetErrorMessage())
	}
	return nil
}

// DeleteSchedulers deletes several schedulers by ID in one call.
func (c *Client) DeleteSchedulers(ctx context.Context, schedulerIDs []string) error {
	resp, err := c.grpc.DeleteSchedulers(ctx, &schedulerpb.DeleteSchedulersRequest{Base: requestBase(), SchedulerIds: schedulerIDs})
	if err != nil {
		return fmt.Errorf("connector: DeleteSchedulers: %w", err)
	}
	if resp.GetHasErrors() {
		return fmt.Errorf("connector: DeleteSchedulers: %s", resp.GetError().GetErrorMessage())
	}
	return nil
}

// DeleteSchedulersByTask deletes every scheduler attached to taskID. 1.3.0-only -- retired from
// ConnectorService on main/2.0.0 along with the rest of the Mission/Task model.
func (c *Client) DeleteSchedulersByTask(ctx context.Context, taskID string) error {
	resp, err := c.grpc.DeleteSchedulersByTask(ctx, &schedulerpb.DeleteSchedulersByTaskRequest{Base: requestBase(), TaskId: taskID})
	if err != nil {
		return fmt.Errorf("connector: DeleteSchedulersByTask(%s): %w", taskID, err)
	}
	if resp.GetHasErrors() {
		return fmt.Errorf("connector: DeleteSchedulersByTask(%s): %s", taskID, resp.GetError().GetErrorMessage())
	}
	return nil
}
