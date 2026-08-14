package connector

import (
	"context"
	"fmt"

	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
)

// GetActivePoliciesByType fetches every active operational policy of a given type.
func (c *Client) GetActivePoliciesByType(ctx context.Context, policyType string) ([]*connectorpb.PolicyProtoDTO, error) {
	resp, err := c.grpc.GetActivePoliciesByType(ctx, &connectorpb.ConnectorGetPoliciesRequest{Base: requestBase(), PolicyType: policyType})
	if err != nil {
		return nil, fmt.Errorf("connector: GetActivePoliciesByType(%s): %w", policyType, err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: GetActivePoliciesByType(%s): %s", policyType, resp.GetError().GetErrorMessage())
	}
	return resp.GetPolicyList().GetPolicies(), nil
}

// GetAllActivePolicies fetches every active operational policy, regardless of type.
func (c *Client) GetAllActivePolicies(ctx context.Context) ([]*connectorpb.PolicyProtoDTO, error) {
	resp, err := c.grpc.GetAllActivePolicies(ctx, &connectorpb.ConnectorGetAllPoliciesRequest{Base: requestBase()})
	if err != nil {
		return nil, fmt.Errorf("connector: GetAllActivePolicies: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: GetAllActivePolicies: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetPolicyList().GetPolicies(), nil
}

// GetTechnicalConfigs fetches technical/runtime configuration entries, optionally scoped by
// scope/scopeTarget (pass "" for either to omit that filter).
func (c *Client) GetTechnicalConfigs(ctx context.Context, scope, scopeTarget string) ([]*connectorpb.TechnicalConfigProtoDTO, error) {
	req := &connectorpb.ConnectorGetConfigsRequest{Base: requestBase()}
	if scope != "" {
		req.Scope = &scope
	}
	if scopeTarget != "" {
		req.ScopeTarget = &scopeTarget
	}
	resp, err := c.grpc.GetTechnicalConfigs(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("connector: GetTechnicalConfigs: %w", err)
	}
	if resp.GetHasErrors() {
		return nil, fmt.Errorf("connector: GetTechnicalConfigs: %s", resp.GetError().GetErrorMessage())
	}
	return resp.GetConfigList().GetConfigs(), nil
}
