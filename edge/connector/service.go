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

	"github.com/zequent/zqnt-edge-sdk-go/v3/adapter/domains"
)

// ConnectorService is the client-side interface for the Connector backend service.
// All methods return (result, error); callers wrap in goroutines for concurrency.
type ConnectorService interface {
	// Asset operations
	GetAssetBySN(ctx context.Context, sn string) (*domains.AssetDTO, error)
	GetAssetByID(ctx context.Context, id string) (*domains.AssetDTO, error)
	GetSubAssetBySN(ctx context.Context, sn string) (*domains.SubAssetDTO, error)
	UpdateAsset(ctx context.Context, id string, asset *domains.AssetDTO) (*domains.AssetDTO, error)
	// Deprecated: an adapter does not create assets. RegisterAsset is insert-only and creates an
	// asset with no organization, which no tenant sees and nobody can move afterwards. Use
	// EnsureAsset: bind to what exists, or pair an unknown serial with a claim code.
	RegisterAsset(ctx context.Context, asset *domains.AssetDTO) (*domains.AssetDTO, error)
	DeRegisterAsset(ctx context.Context, id string) (bool, error)

	// RedeemAssetClaim trades a one-time claim code for the asset and returns it. The code is the
	// credential, and the claim, never asset.Organization, decides the organization the asset
	// lands in. A refused code returns (nil, nil): unknown, expired, revoked, used up and not valid
	// for this kind of device all answer alike, so the call cannot probe which codes exist.
	RedeemAssetClaim(ctx context.Context, code string, asset *domains.AssetDTO) (*domains.AssetDTO, error)
	// DescribeAssetClaim returns the name of the organization a claim code would provision into,
	// without spending it, so a device can show its operator where it is about to bind. Every
	// unusable code returns ("", nil).
	DescribeAssetClaim(ctx context.Context, code string) (string, error)
	// EnsureAsset looks asset.SN up and redeems claimCode only when the platform does not know it.
	// The lookup comes first because a claim is single-use: after the first pairing there is
	// nothing left to redeem. With no code it reports what it found and creates nothing, the
	// resting state for assets provisioned in the console.
	EnsureAsset(ctx context.Context, asset *domains.AssetDTO, claimCode string) (*domains.AssetDTO, error)

	// Organization operations
	GetOrganizationByID(ctx context.Context, id string) (*domains.OrganizationDTO, error)
}
