// Package missionautonomy is a client for MissionAutonomyService's Application and SkillExecution
// surface — capability package administration and the full execution lifecycle (create, start,
// pause, resume, cancel, signal) — mirroring client-java-sdk's MissionAutonomy interface for the
// same RPCs. Scheduler and the deprecated Mission/Task RPCs are intentionally not covered here.
package missionautonomy

import (
	"context"
	"fmt"
	"time"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	execdto "github.com/Zequent/zqnt-client-sdk-go/gen/execution/contracts/proto"
	execution "github.com/Zequent/zqnt-client-sdk-go/gen/execution/dto/proto"
	missionautonomypb "github.com/Zequent/zqnt-client-sdk-go/gen/missionautonomy/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
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

// UpsertApplication creates or updates a capability package. Set expectedRevision to guard
// against a concurrent update (optimistic concurrency); leave it empty to skip the check.
func (c *Client) UpsertApplication(ctx context.Context, app *execution.ApplicationProtoDTO, expectedRevision string) (*execution.ApplicationProtoDTO, error) {
	req := &execdto.UpsertApplicationRequest{Base: requestBase(), Application: app}
	if expectedRevision != "" {
		req.ExpectedRevision = &expectedRevision
	}
	resp, err := c.grpc.UpsertApplication(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: UpsertApplication(%s): %w", app.GetId(), err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: UpsertApplication(%s): %s", app.GetId(), resp.GetError().GetErrorMessage())
	}
	return resp.GetApplication(), nil
}

// GetApplication fetches a capability package by ID. version is optional — omit it (empty
// string) for the latest version.
func (c *Client) GetApplication(ctx context.Context, applicationID, version string) (*execution.ApplicationProtoDTO, error) {
	req := &execdto.GetApplicationRequest{Base: requestBase(), ApplicationId: applicationID}
	if version != "" {
		req.Version = &version
	}
	resp, err := c.grpc.GetApplication(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: GetApplication(%s): %w", applicationID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: GetApplication(%s): %s", applicationID, resp.GetError().GetErrorMessage())
	}
	return resp.GetApplication(), nil
}

// DeleteApplication deletes a capability package. version and expectedRevision are both
// optional — pass "" to omit either.
func (c *Client) DeleteApplication(ctx context.Context, applicationID, version, expectedRevision string) error {
	req := &execdto.DeleteApplicationRequest{Base: requestBase(), ApplicationId: applicationID}
	if version != "" {
		req.Version = &version
	}
	if expectedRevision != "" {
		req.ExpectedRevision = &expectedRevision
	}
	resp, err := c.grpc.DeleteApplication(ctx, req)
	if err != nil {
		return fmt.Errorf("missionautonomy: DeleteApplication(%s): %w", applicationID, err)
	}
	if resp.GetHasErrors() {
		return fmt.Errorf("missionautonomy: DeleteApplication(%s): %s", applicationID, resp.GetError().GetErrorMessage())
	}
	return nil
}

// CreateSimpleExecution creates (but does not start) a single-command execution — call Start to
// actually dispatch it, or use ExecuteSimple to do both atomically. idempotencyKey makes repeated
// calls with the same asset SN + key return the original execution instead of creating a new one.
func (c *Client) CreateSimpleExecution(ctx context.Context, assetSn, commandID string, parameters *structpb.Struct, idempotencyKey string) (*execution.SkillExecutionProtoDTO, error) {
	req := &execdto.CreateSkillExecutionRequest{
		Base:           &base.RequestBase{Tid: newTid(), Sn: assetSn, Timestamp: timestamppb.Now()},
		Spec:           simpleSpec(commandID, parameters),
		IdempotencyKey: idempotencyKey,
	}
	resp, err := c.grpc.CreateSkillExecution(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: CreateSkillExecution(%s): %w", commandID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: CreateSkillExecution(%s): %s", commandID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

// ExecuteSimple creates and atomically starts a single-command execution (the "auto_start"
// convenience RPC) — the common case when you just want the command to run immediately.
func (c *Client) ExecuteSimple(ctx context.Context, assetSn, commandID string, parameters *structpb.Struct, idempotencyKey string) (*execution.SkillExecutionProtoDTO, error) {
	req := &execdto.ExecuteSkillRequest{
		Base:           &base.RequestBase{Tid: newTid(), Sn: assetSn, Timestamp: timestamppb.Now()},
		Spec:           simpleSpec(commandID, parameters),
		IdempotencyKey: idempotencyKey,
	}
	resp, err := c.grpc.ExecuteSkill(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: ExecuteSkill(%s): %w", commandID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: ExecuteSkill(%s): %s", commandID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

// GetSkillExecution fetches an execution by ID.
func (c *Client) GetSkillExecution(ctx context.Context, executionID string) (*execution.SkillExecutionProtoDTO, error) {
	resp, err := c.grpc.GetSkillExecution(ctx, &execdto.GetSkillExecutionRequest{Base: requestBase(), ExecutionId: executionID})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: GetSkillExecution(%s): %w", executionID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: GetSkillExecution(%s): %s", executionID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

// Start/Pause/Resume/Cancel drive an execution's lifecycle.
func (c *Client) StartSkillExecution(ctx context.Context, executionID string) (*execution.SkillExecutionProtoDTO, error) {
	return c.lifecycle(ctx, "StartSkillExecution", executionID, c.grpc.StartSkillExecution)
}
func (c *Client) PauseSkillExecution(ctx context.Context, executionID string) (*execution.SkillExecutionProtoDTO, error) {
	return c.lifecycle(ctx, "PauseSkillExecution", executionID, c.grpc.PauseSkillExecution)
}
func (c *Client) ResumeSkillExecution(ctx context.Context, executionID string) (*execution.SkillExecutionProtoDTO, error) {
	return c.lifecycle(ctx, "ResumeSkillExecution", executionID, c.grpc.ResumeSkillExecution)
}
func (c *Client) CancelSkillExecution(ctx context.Context, executionID string) (*execution.SkillExecutionProtoDTO, error) {
	return c.lifecycle(ctx, "CancelSkillExecution", executionID, c.grpc.CancelSkillExecution)
}

type lifecycleRPC func(context.Context, *execdto.SkillExecutionLifecycleRequest, ...grpc.CallOption) (*execdto.SkillExecutionResponse, error)

func (c *Client) lifecycle(ctx context.Context, op, executionID string, rpc lifecycleRPC) (*execution.SkillExecutionProtoDTO, error) {
	resp, err := rpc(ctx, &execdto.SkillExecutionLifecycleRequest{Base: requestBase(), ExecutionId: executionID})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: %s(%s): %w", op, executionID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: %s(%s): %s", op, executionID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

// SignalSkillExecution delivers an external event or a human-approval decision to a waiting node.
// nodeID and eventType are optional (pass "" to omit); approved is only meaningful for a
// HUMAN_APPROVAL node.
func (c *Client) SignalSkillExecution(ctx context.Context, executionID, nodeID, eventType string, data *structpb.Struct, approved *bool) (*execution.SkillExecutionProtoDTO, error) {
	req := &execdto.SignalSkillExecutionRequest{Base: requestBase(), ExecutionId: executionID, Data: data, Approved: approved}
	if nodeID != "" {
		req.NodeId = &nodeID
	}
	if eventType != "" {
		req.EventType = &eventType
	}
	resp, err := c.grpc.SignalSkillExecution(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: SignalSkillExecution(%s): %w", executionID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: SignalSkillExecution(%s): %s", executionID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

func simpleSpec(commandID string, parameters *structpb.Struct) *execution.SkillExecutionSpecProto {
	return &execution.SkillExecutionSpecProto{
		Execution: &execution.SkillExecutionSpecProto_Simple{
			Simple: &execution.SimpleExecutionSpecProto{CommandId: commandID, Parameters: parameters},
		},
	}
}

// applicationSpec builds a SkillExecutionSpecProto that runs a named Skill out of a deployed
// Application, instead of a single ad-hoc command — the ApplicationExecutionSpecProto branch of
// the execution spec's oneof. applicationVersion is optional ("" runs the latest version).
func applicationSpec(applicationID, skillID, applicationVersion string, parameters *structpb.Struct) *execution.SkillExecutionSpecProto {
	spec := &execution.ApplicationExecutionSpecProto{ApplicationId: applicationID, SkillId: skillID, Parameters: parameters}
	if applicationVersion != "" {
		spec.ApplicationVersion = &applicationVersion
	}
	return &execution.SkillExecutionSpecProto{
		Execution: &execution.SkillExecutionSpecProto_Application{Application: spec},
	}
}

// CreateApplicationExecution creates (but does not start) an execution of one Skill from a
// deployed Application — the counterpart to CreateSimpleExecution for named, versioned Skills
// rather than single ad-hoc commands.
func (c *Client) CreateApplicationExecution(ctx context.Context, assetSn, applicationID, skillID, applicationVersion string, parameters *structpb.Struct, idempotencyKey string) (*execution.SkillExecutionProtoDTO, error) {
	req := &execdto.CreateSkillExecutionRequest{
		Base:           &base.RequestBase{Tid: newTid(), Sn: assetSn, Timestamp: timestamppb.Now()},
		Spec:           applicationSpec(applicationID, skillID, applicationVersion, parameters),
		IdempotencyKey: idempotencyKey,
	}
	resp, err := c.grpc.CreateSkillExecution(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: CreateSkillExecution(%s/%s): %w", applicationID, skillID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: CreateSkillExecution(%s/%s): %s", applicationID, skillID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

// ExecuteApplication creates and atomically starts an execution of one Skill from a deployed
// Application — the counterpart to ExecuteSimple for named, versioned Skills.
func (c *Client) ExecuteApplication(ctx context.Context, assetSn, applicationID, skillID, applicationVersion string, parameters *structpb.Struct, idempotencyKey string) (*execution.SkillExecutionProtoDTO, error) {
	req := &execdto.ExecuteSkillRequest{
		Base:           &base.RequestBase{Tid: newTid(), Sn: assetSn, Timestamp: timestamppb.Now()},
		Spec:           applicationSpec(applicationID, skillID, applicationVersion, parameters),
		IdempotencyKey: idempotencyKey,
	}
	resp, err := c.grpc.ExecuteSkill(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: ExecuteSkill(%s/%s): %w", applicationID, skillID, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: ExecuteSkill(%s/%s): %s", applicationID, skillID, resp.GetError().GetErrorMessage())
	}
	return resp.GetExecution(), nil
}

// ListApplications lists deployed capability packages, optionally scoped and/or filtered to
// enabled-only. Pass scope=nil for every scope; pageSize=0/pageToken="" to use RPC defaults.
func (c *Client) ListApplications(ctx context.Context, scope *execution.ApplicationScopeProtoDTO, enabledOnly bool, pageSize int32, pageToken string) (apps []*execution.ApplicationProtoDTO, nextPageToken string, err error) {
	req := &execdto.ListApplicationsRequest{Base: requestBase(), Scope: scope}
	if enabledOnly {
		req.EnabledOnly = &enabledOnly
	}
	if pageSize != 0 {
		req.PageSize = &pageSize
	}
	if pageToken != "" {
		req.PageToken = &pageToken
	}
	resp, err := c.grpc.ListApplications(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("missionautonomy: ListApplications: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, "", fmt.Errorf("missionautonomy: ListApplications: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetResult().GetApplications(), resp.GetResult().GetNextPageToken(), nil
}

// ListSkillExecutions lists executions, filtered by any combination of the optional fields on
// query (pass "" / nil to omit a filter).
func (c *Client) ListSkillExecutions(ctx context.Context, query *execdto.ListSkillExecutionsRequest) (executions []*execution.SkillExecutionProtoDTO, nextPageToken string, err error) {
	if query == nil {
		query = &execdto.ListSkillExecutionsRequest{}
	}
	query.Base = requestBase()
	resp, err := c.grpc.ListSkillExecutions(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("missionautonomy: ListSkillExecutions: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, "", fmt.Errorf("missionautonomy: ListSkillExecutions: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetResult().GetExecutions(), resp.GetResult().GetNextPageToken(), nil
}

// ResolveExecutionConfig resolves effective config values for context, restricted to keys when
// non-empty (pass nil/empty to resolve every known key).
func (c *Client) ResolveExecutionConfig(ctx context.Context, execContext *execution.ExecutionConfigContextProto, keys []string) (*execution.ResolvedExecutionConfigProtoDTO, error) {
	resp, err := c.grpc.ResolveExecutionConfig(ctx, &execdto.ResolveExecutionConfigRequest{Base: requestBase(), Context: execContext, Keys: keys})
	if err != nil {
		return nil, fmt.Errorf("missionautonomy: ResolveExecutionConfig: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("missionautonomy: ResolveExecutionConfig: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetConfig(), nil
}

func requestBase() *base.RequestBase {
	return &base.RequestBase{Tid: newTid(), Timestamp: timestamppb.Now()}
}

func newTid() string {
	return fmt.Sprintf("client-go-sdk-%d", time.Now().UnixNano())
}
