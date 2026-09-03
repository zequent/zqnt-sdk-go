package connector

import (
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"
	assetpb "github.com/Zequent/zqnt-edge-sdk-go/gen/common/asset/proto"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Mapper converts between proto DTO messages and domain DTO structs for the
// ConnectorService.
type Mapper struct{}

// ---- proto → domain ---------------------------------------------------------

func (m *Mapper) AssetFromProto(p *assetpb.AssetProtoDTO) *domains.AssetDTO {
	if p == nil {
		return nil
	}
	dto := &domains.AssetDTO{
		ID:                     p.Id,
		SN:                     p.Sn,
		Name:                   p.Name,
		Type:                   enumString(p.Type),
		Vendor:                 enumString(p.Vendor),
		Connection:             enumString(p.Connection),
		SystemConnectionString: p.SystemConnectionString,
		Model:                  p.Model,
		ExternalDeviceType:     p.ExternalDeviceType,
		ExternalDeviceSubType:  p.ExternalDeviceSubType,
		Organization:           p.Organization,
		ExternalID:             p.ExternalId,
		ModifiedFrom:           p.ModifiedFrom,
		LiveStreamPushURL:      p.LiveStreamPushUrl,
		LiveStreamPullURL:      p.LiveStreamPullUrl,
		CreatedAt:              tPtr(p.CreatedAt),
		ModifiedAt:             tPtr(p.ModifiedAt),
	}
	for _, sa := range p.SubAssets {
		if d := m.SubAssetFromProto(sa); d != nil {
			dto.SubAssets = append(dto.SubAssets, *d)
		}
	}
	for _, pl := range p.Payloads {
		if d := m.PayloadFromProto(pl); d != nil {
			dto.Payloads = append(dto.Payloads, *d)
		}
	}
	return dto
}

func (m *Mapper) SubAssetFromProto(p *assetpb.SubAssetProtoDTO) *domains.SubAssetDTO {
	if p == nil {
		return nil
	}
	dto := &domains.SubAssetDTO{
		ID:                     p.Id,
		SN:                     p.Sn,
		Name:                   p.Name,
		Type:                   enumString(p.Type),
		Vendor:                 enumString(p.Vendor),
		Connection:             enumString(p.Connection),
		SystemConnectionString: p.SystemConnectionString,
		Model:                  p.Model,
		ExternalDeviceType:     p.ExternalDeviceType,
		ExternalDeviceSubType:  p.ExternalDeviceSubType,
		ExternalID:             p.ExternalId,
		StreamURLPredefined:    p.StreamUrlPredefined,
		ModifiedFrom:           p.ModifiedFrom,
		LiveStreamPushURL:      p.LiveStreamPushUrl,
		LiveStreamPullURL:      p.LiveStreamPullUrl,
		CreatedAt:              tPtr(p.CreatedAt),
		ModifiedAt:             tPtr(p.ModifiedAt),
	}
	for _, pl := range p.Payloads {
		if d := m.PayloadFromProto(pl); d != nil {
			dto.Payloads = append(dto.Payloads, *d)
		}
	}
	return dto
}

func (m *Mapper) PayloadFromProto(p *assetpb.AssetPayloadProtoDTO) *domains.AssetPayloadDTO {
	if p == nil {
		return nil
	}
	return &domains.AssetPayloadDTO{
		ID:              p.Id,
		ExternalID:      p.ExternalId,
		ExternalType:    p.ExternalType,
		SlotIndex:       p.SlotIndex,
		Name:            p.Name,
		SerialNumber:    p.SerialNumber,
		Kind:            p.Kind,
		Vendor:          p.Vendor,
		Model:           p.Model,
		FirmwareVersion: p.FirmwareVersion,
		LibraryVersion:  p.LibraryVersion,
		Active:          p.Active,
		PayloadRef:      p.PayloadRef,
		LastSeenAt:      tPtr(p.LastSeenAt),
		CreatedAt:       tPtr(p.CreatedAt),
		ModifiedAt:      tPtr(p.ModifiedAt),
	}
}

func (m *Mapper) OrgFromProto(p *assetpb.OrganizationProtoDTO) *domains.OrganizationDTO {
	if p == nil {
		return nil
	}
	return &domains.OrganizationDTO{
		ID:          p.GetId(),
		Name:        p.Name,
		Description: p.Description,
		Assets:      p.Assets,
	}
}

// ---- domain → proto ---------------------------------------------------------

func (m *Mapper) AssetToProto(dto *domains.AssetDTO) *assetpb.AssetProtoDTO {
	if dto == nil {
		return nil
	}
	p := &assetpb.AssetProtoDTO{
		Id:                     dto.ID,
		Sn:                     dto.SN,
		Name:                   dto.Name,
		Type:                   parseEnum[assetpb.AssetTypeEnum](assetpb.AssetTypeEnum_value, dto.Type),
		Vendor:                 parseEnum[assetpb.AssetVendor](assetpb.AssetVendor_value, dto.Vendor),
		Connection:             parseEnum[assetpb.AssetConnection](assetpb.AssetConnection_value, dto.Connection),
		SystemConnectionString: dto.SystemConnectionString,
		Model:                  dto.Model,
		ExternalDeviceType:     dto.ExternalDeviceType,
		ExternalDeviceSubType:  dto.ExternalDeviceSubType,
		Organization:           dto.Organization,
		ExternalId:             dto.ExternalID,
		ModifiedFrom:           dto.ModifiedFrom,
		LiveStreamPushUrl:      dto.LiveStreamPushURL,
		LiveStreamPullUrl:      dto.LiveStreamPullURL,
	}
	for _, sa := range dto.SubAssets {
		sa := sa
		p.SubAssets = append(p.SubAssets, m.SubAssetToProto(&sa))
	}
	for _, pl := range dto.Payloads {
		pl := pl
		p.Payloads = append(p.Payloads, m.PayloadToProto(&pl))
	}
	return p
}

func (m *Mapper) SubAssetToProto(dto *domains.SubAssetDTO) *assetpb.SubAssetProtoDTO {
	if dto == nil {
		return nil
	}
	p := &assetpb.SubAssetProtoDTO{
		Id:                     dto.ID,
		Sn:                     dto.SN,
		Name:                   dto.Name,
		Type:                   parseEnum[assetpb.AssetTypeEnum](assetpb.AssetTypeEnum_value, dto.Type),
		Vendor:                 parseEnum[assetpb.AssetVendor](assetpb.AssetVendor_value, dto.Vendor),
		Connection:             parseEnum[assetpb.AssetConnection](assetpb.AssetConnection_value, dto.Connection),
		SystemConnectionString: dto.SystemConnectionString,
		Model:                  dto.Model,
		ExternalDeviceType:     dto.ExternalDeviceType,
		ExternalDeviceSubType:  dto.ExternalDeviceSubType,
		ExternalId:             dto.ExternalID,
		StreamUrlPredefined:    dto.StreamURLPredefined,
		ModifiedFrom:           dto.ModifiedFrom,
		LiveStreamPushUrl:      dto.LiveStreamPushURL,
		LiveStreamPullUrl:      dto.LiveStreamPullURL,
	}
	for _, pl := range dto.Payloads {
		pl := pl
		p.Payloads = append(p.Payloads, m.PayloadToProto(&pl))
	}
	return p
}

func (m *Mapper) PayloadToProto(dto *domains.AssetPayloadDTO) *assetpb.AssetPayloadProtoDTO {
	if dto == nil {
		return nil
	}
	return &assetpb.AssetPayloadProtoDTO{
		Id:              dto.ID,
		ExternalId:      dto.ExternalID,
		ExternalType:    dto.ExternalType,
		SlotIndex:       dto.SlotIndex,
		Name:            dto.Name,
		SerialNumber:    dto.SerialNumber,
		Kind:            dto.Kind,
		Vendor:          dto.Vendor,
		Model:           dto.Model,
		FirmwareVersion: dto.FirmwareVersion,
		LibraryVersion:  dto.LibraryVersion,
		Active:          dto.Active,
		PayloadRef:      dto.PayloadRef,
	}
}

// ---- helpers ------------------------------------------------------------------

type stringer interface{ String() string }

// enumString safely stringifies a nilable proto enum pointer (every scalar Asset/SubAsset enum
// field is `optional` in the current schema, so it arrives as e.g. *AssetVendor, not AssetVendor).
// Returns "" for nil rather than panicking on a nil dereference.
func enumString[T stringer](e *T) string {
	if e == nil {
		return ""
	}
	return (*e).String()
}

// parseEnum resolves a domain enum-name string (e.g. "ASSET_VENDOR_ZQNT") back to its proto enum
// pointer via protoc-gen-go's generated *_value name->number map, the inverse of enumString.
//
// AssetToProto/SubAssetToProto never set Type/Vendor/Connection before this: RegisterAsset and
// UpdateAsset silently sent every asset with those three fields unset, which the backend reads as
// the enum zero value (AssetTypeEnum_UNKNOWN / AssetVendor_DJI / AssetConnection's own zero) rather
// than an error -- a real asset registered through this SDK would misreport as DJI regardless of
// its actual vendor. Found while wiring the v1.3.0-compat simulator's own RegisterAsset call.
func parseEnum[T ~int32](values map[string]int32, s string) *T {
	if s == "" {
		return nil
	}
	n, ok := values[s]
	if !ok {
		return nil
	}
	v := T(n)
	return &v
}

func tPtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
