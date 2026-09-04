// Package connector provides the ConnectorService interface and its gRPC-backed
// implementation for managing assets and organizations.
//
// Mission/Task/Scheduler CRUD was removed from this interface 2026-09-02: the platform's
// Skill/Application migration deleted the corresponding RPCs from connector.proto entirely (the
// backend no longer has Mission/Task/Scheduler CRUD at all -- that model was replaced by
// MissionAutonomyService's Application/Skill-execution API). See missionautonomy/ for the current
// equivalent.
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

	// Organization operations
	GetOrganizationByID(ctx context.Context, id string) (*domains.OrganizationDTO, error)
}
