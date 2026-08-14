package connector

import (
	"context"
	"testing"

	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
)

type fakePolicyService struct {
	connectorpb.UnimplementedConnectorServiceServer
	lastPoliciesReq *connectorpb.ConnectorGetPoliciesRequest
	lastConfigsReq  *connectorpb.ConnectorGetConfigsRequest
}

func (s *fakePolicyService) GetActivePoliciesByType(ctx context.Context, req *connectorpb.ConnectorGetPoliciesRequest) (*connectorpb.ConnectorPolicyResponse, error) {
	s.lastPoliciesReq = req
	return &connectorpb.ConnectorPolicyResponse{
		Response: &connectorpb.ConnectorPolicyResponse_PolicyList{
			PolicyList: &connectorpb.PolicyProtoDTOList{Policies: []*connectorpb.PolicyProtoDTO{{PolicyType: req.GetPolicyType()}}},
		},
	}, nil
}

func (s *fakePolicyService) GetTechnicalConfigs(ctx context.Context, req *connectorpb.ConnectorGetConfigsRequest) (*connectorpb.ConnectorConfigResponse, error) {
	s.lastConfigsReq = req
	return &connectorpb.ConnectorConfigResponse{
		Response: &connectorpb.ConnectorConfigResponse_ConfigList{
			ConfigList: &connectorpb.TechnicalConfigProtoDTOList{Configs: []*connectorpb.TechnicalConfigProtoDTO{{ConfigKey: "geofence.enabled"}}},
		},
	}, nil
}

func TestGetActivePoliciesByTypeFiltersByType(t *testing.T) {
	fake := &fakePolicyService{}
	client := dialFake(t, fake)

	got, err := client.GetActivePoliciesByType(context.Background(), "GEOFENCE")
	if err != nil {
		t.Fatalf("GetActivePoliciesByType: %v", err)
	}
	if len(got) != 1 || got[0].GetPolicyType() != "GEOFENCE" {
		t.Fatalf("expected one GEOFENCE policy, got %v", got)
	}
	if fake.lastPoliciesReq.GetPolicyType() != "GEOFENCE" {
		t.Fatalf("expected policyType to be sent, got %q", fake.lastPoliciesReq.GetPolicyType())
	}
}

func TestGetTechnicalConfigsScopesTheRequest(t *testing.T) {
	fake := &fakePolicyService{}
	client := dialFake(t, fake)

	got, err := client.GetTechnicalConfigs(context.Background(), "ORGANIZATION", "org-1")
	if err != nil {
		t.Fatalf("GetTechnicalConfigs: %v", err)
	}
	if len(got) != 1 || got[0].GetConfigKey() != "geofence.enabled" {
		t.Fatalf("expected one geofence.enabled config, got %v", got)
	}
	if fake.lastConfigsReq.GetScope() != "ORGANIZATION" || fake.lastConfigsReq.GetScopeTarget() != "org-1" {
		t.Fatalf("expected scope/scopeTarget to be sent, got %v", fake.lastConfigsReq)
	}
}
