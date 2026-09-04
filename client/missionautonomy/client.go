// Package missionautonomy is a client for MissionAutonomyService's Mission/Task/Scheduler
// surface, as it exists at the 1.3.0 wire contract this branch tracks — mirroring
// client-java-sdk's MissionAutonomy interface for the same RPCs.
//
// This branch is the mirror image of main: main's MissionAutonomyService surface is
// Application/SkillExecution (capability-execution-*.proto, which doesn't exist at 1.3.0 at
// all) and main explicitly does NOT cover Mission/Task ("There was nothing working to mirror,
// so this package doesn't have them" — accurate for main, not for 1.3.0, where GetMission/
// CreateMission/.../StartTask/StopTask/PauseTask/ResumeTask are all real RPCs). Scheduler CRUD
// lives in connector.Client (identical wire messages on both services, at both contract
// versions) — unchanged from main.
//
// See zqnt-protos' README "Versioning" section.
package missionautonomy

import (
	"context"
	"fmt"
	"time"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	missionautonomycontracts "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/contracts/proto"
	missionautonomydto "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/dto/proto"
	missionautonomypb "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Client wraps MissionAutonomyServiceClient, converting the wire-level has_errors/error convention
// into an idiomatic Go error return.
type Client struct {
	grpc missionautonomypb.MissionAutonomyServiceClient
}

// New wraps an existing gRPC connection — dial it yourself with your own retry/TLS policy.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{grpc: missionautonomypb.NewMissionAutonomyServiceClient(conn)}
}

// CreateMission creates a mission.
func (c *Client) CreateMission(ctx context.Context, mission *missionautonomydto.MissionProtoDTO) (*missionautonomydto.MissionProtoDTO, error) {
	resp, err := c.grpc.CreateMission(ctx, &missionautonomycontracts.CreateMissionRequest{Base: requestBase(), Mission: mission})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: CreateMission(%s): %w", mission.GetName(), err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: CreateMission(%s): %s", mission.GetName(), resp.GetError().GetErrorMessage())
	}
	return resp.GetMission(), nil
}

// UpdateMission updates a mission by ID.
func (c *Client) UpdateMission(ctx context.Context, missionID string, mission *missionautonomydto.MissionProtoDTO) (*missionautonomydto.MissionProtoDTO, error) {
	resp, err := c.grpc.UpdateMission(ctx, &missionautonomycontracts.UpdateMissionRequest{Base: requestBase(), MissionId: missionID, Mission: mission})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: UpdateMission(%s): %w", missionID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: UpdateMission(%s): %s", missionID, resp.GetError().GetErrorMessage())
	}
	return resp.GetMission(), nil
}

// GetMission fetches a mission by ID.
func (c *Client) GetMission(ctx context.Context, missionID string) (*missionautonomydto.MissionProtoDTO, error) {
	resp, err := c.grpc.GetMission(ctx, &missionautonomycontracts.GetMissionRequest{Base: requestBase(), MissionId: missionID})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: GetMission(%s): %w", missionID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: GetMission(%s): %s", missionID, resp.GetError().GetErrorMessage())
	}
	return resp.GetMission(), nil
}

// DeleteMission deletes a mission by ID.
func (c *Client) DeleteMission(ctx context.Context, missionID string) error {
	resp, err := c.grpc.DeleteMission(ctx, &missionautonomycontracts.DeleteMissionRequest{Base: requestBase(), MissionId: missionID})
	if err != nil {
		return fmt.Errorf("missionautonomy: DeleteMission(%s): %w", missionID, err)
	}
	if resp.GetHasErrors() {
		return fmt.Errorf("missionautonomy: DeleteMission(%s): %s", missionID, resp.GetError().GetErrorMessage())
	}
	return nil
}

// CreateTask creates a task.
func (c *Client) CreateTask(ctx context.Context, task *missionautonomydto.TaskProtoDTO) (*missionautonomydto.TaskProtoDTO, error) {
	resp, err := c.grpc.CreateTask(ctx, &missionautonomycontracts.CreateTaskRequest{Base: requestBase(), Task: task})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: CreateTask(%s): %w", task.GetName(), err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: CreateTask(%s): %s", task.GetName(), resp.GetError().GetErrorMessage())
	}
	return resp.GetTask(), nil
}

// UpdateTask updates a task by ID.
func (c *Client) UpdateTask(ctx context.Context, taskID string, task *missionautonomydto.TaskProtoDTO) (*missionautonomydto.TaskProtoDTO, error) {
	resp, err := c.grpc.UpdateTask(ctx, &missionautonomycontracts.UpdateTaskRequest{Base: requestBase(), TaskId: taskID, Task: task})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: UpdateTask(%s): %w", taskID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: UpdateTask(%s): %s", taskID, resp.GetError().GetErrorMessage())
	}
	return resp.GetTask(), nil
}

// GetTask fetches a task by ID.
func (c *Client) GetTask(ctx context.Context, taskID string) (*missionautonomydto.TaskProtoDTO, error) {
	resp, err := c.grpc.GetTask(ctx, &missionautonomycontracts.GetTaskRequest{Base: requestBase(), TaskId: taskID})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: GetTask(%s): %w", taskID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: GetTask(%s): %s", taskID, resp.GetError().GetErrorMessage())
	}
	return resp.GetTask(), nil
}

// GetTaskByFlightID fetches a task by its flight ID (waypoint config flightId, e.g. a DJI/
// Mavlink external mission identifier).
func (c *Client) GetTaskByFlightID(ctx context.Context, flightID string) (*missionautonomydto.TaskProtoDTO, error) {
	resp, err := c.grpc.GetTaskByFlightId(ctx, &missionautonomycontracts.GetTaskByFlightIdRequest{Base: requestBase(), FlightId: flightID})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: GetTaskByFlightId(%s): %w", flightID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: GetTaskByFlightId(%s): %s", flightID, resp.GetError().GetErrorMessage())
	}
	return resp.GetTask(), nil
}

// DeleteTask deletes a task by ID.
func (c *Client) DeleteTask(ctx context.Context, taskID string) error {
	resp, err := c.grpc.DeleteTask(ctx, &missionautonomycontracts.DeleteTaskRequest{Base: requestBase(), TaskId: taskID})
	if err != nil {
		return fmt.Errorf("missionautonomy: DeleteTask(%s): %w", taskID, err)
	}
	if resp.GetHasErrors() {
		return fmt.Errorf("missionautonomy: DeleteTask(%s): %s", taskID, resp.GetError().GetErrorMessage())
	}
	return nil
}

// ListSchedulers lists every scheduler, optionally filtered to one task's. taskID="" means no
// filter. Lives here, not on connector.Client, because ConnectorService has no ListSchedulers RPC
// at the 1.3.0 wire contract this branch tracks -- see connector/scheduler.go's own note.
func (c *Client) ListSchedulers(ctx context.Context, taskID string) ([]*missionautonomydto.SchedulerProtoDTO, error) {
	req := &missionautonomycontracts.ListSchedulersRequest{Base: requestBase()}
	if taskID != "" {
		req.TaskId = &taskID
	}
	resp, err := c.grpc.ListSchedulers(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: ListSchedulers: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: ListSchedulers: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetSchedulers().GetSchedulerDtoList(), nil
}

// Start/Stop/Pause/Resume drive a task's lifecycle.
func (c *Client) StartTask(ctx context.Context, taskID string) (*missionautonomydto.TaskProtoDTO, error) {
	return c.taskLifecycle(ctx, "StartTask", taskID, c.grpc.StartTask)
}
func (c *Client) StopTask(ctx context.Context, taskID string) (*missionautonomydto.TaskProtoDTO, error) {
	return c.taskLifecycle(ctx, "StopTask", taskID, c.grpc.StopTask)
}
func (c *Client) PauseTask(ctx context.Context, taskID string) (*missionautonomydto.TaskProtoDTO, error) {
	return c.taskLifecycle(ctx, "PauseTask", taskID, c.grpc.PauseTask)
}
func (c *Client) ResumeTask(ctx context.Context, taskID string) (*missionautonomydto.TaskProtoDTO, error) {
	return c.taskLifecycle(ctx, "ResumeTask", taskID, c.grpc.ResumeTask)
}

type taskLifecycleRPC func(context.Context, *missionautonomycontracts.TaskLifecycleRequest, ...grpc.CallOption) (*missionautonomycontracts.TaskResponse, error)

func (c *Client) taskLifecycle(ctx context.Context, op, taskID string, rpc taskLifecycleRPC) (*missionautonomydto.TaskProtoDTO, error) {
	resp, err := rpc(ctx, &missionautonomycontracts.TaskLifecycleRequest{Base: requestBase(), TaskId: taskID})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: %s(%s): %w", op, taskID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: %s(%s): %s", op, taskID, resp.GetError().GetErrorMessage())
	}
	return resp.GetTask(), nil
}

func requestBase() *base.RequestBase {
	return &base.RequestBase{Tid: newTid(), Timestamp: timestamppb.Now()}
}

func newTid() string {
	return fmt.Sprintf("client-go-sdk-%d", time.Now().UnixNano())
}
