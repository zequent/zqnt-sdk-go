package connector

import (
	"time"

	zqntpb "buf.build/gen/go/zqnt/protos/protocolbuffers/go"
	"github.com/Zequent/zqnt-edge-sdk-go/adapter/domains"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Mapper converts between proto DTO messages and domain DTO structs for the
// ConnectorService.
type Mapper struct{}

// ---- proto → domain ---------------------------------------------------------

func (m *Mapper) AssetFromProto(p *zqntpb.AssetProtoDTO) *domains.AssetDTO {
	if p == nil {
		return nil
	}
	dto := &domains.AssetDTO{
		ID:           p.Id,
		SN:           p.Sn,
		Name:         p.Name,
		Type:         p.Type.String(),
		Vendor:       p.Vendor.String(),
		Connection:   p.Connection.String(),
		Model:        p.Model,
		Organization: p.Organization,
		Online:       p.Online,
		StreamType:   p.StreamType.String(),
	}
	dto.ConnectionString = p.ConnectionString
	dto.Port = p.Port
	dto.LiveStreamServer = p.LiveStreamServer
	dto.ExternalDeviceType = p.ExternalDeviceType
	dto.ExternalDeviceSubType = p.ExternalDeviceSubType
	dto.ExternalID = p.ExternalId
	if p.SubAssetDTO != nil {
		dto.SubAsset = m.SubAssetFromProto(p.SubAssetDTO)
	}
	return dto
}

func (m *Mapper) SubAssetFromProto(p *zqntpb.SubAssetProtoDTO) *domains.SubAssetDTO {
	if p == nil {
		return nil
	}
	dto := &domains.SubAssetDTO{
		ID:           p.Id,
		SN:           p.Sn,
		Name:         p.Name,
		Type:         p.Type.String(),
		Vendor:       p.Vendor.String(),
		Connection:   p.Connection.String(),
		Model:        p.Model,
		Organization: p.Organization,
		Online:       p.Online,
		StreamType:   p.StreamType.String(),
	}
	dto.ConnectionString = p.ConnectionString
	dto.Port = p.Port
	dto.LiveStreamServer = p.LiveStreamServer
	dto.ExternalDeviceType = p.ExternalDeviceType
	dto.ExternalDeviceSubType = p.ExternalDeviceSubType
	dto.ExternalID = p.ExternalId
	dto.StreamURLPredefined = p.StreamUrlPredefined
	return dto
}

func (m *Mapper) OrgFromProto(p *zqntpb.OrganizationProtoDTO) *domains.OrganizationDTO {
	if p == nil {
		return nil
	}
	return &domains.OrganizationDTO{
		ID:          p.Id,
		Name:        p.Name,
		Description: p.Description,
		Assets:      p.Assets,
	}
}

func (m *Mapper) MissionFromProto(p *zqntpb.MissionProtoDTO) *domains.MissionDTO {
	if p == nil {
		return nil
	}
	dto := &domains.MissionDTO{
		Name:           p.Name,
		Description:    p.Description,
		Status:         p.Status.String(),
		Type:           p.Type.String(),
		AssignedAssets: p.AssignedAssets,
	}
	dto.ID = p.Id
	dto.GeoJSON = p.GeoJson
	dto.UpdatedUser = p.UpdatedUser
	if p.StartDate != nil {
		t := p.StartDate.AsTime()
		dto.StartDate = &t
	}
	if p.EndDate != nil {
		t := p.EndDate.AsTime()
		dto.EndDate = &t
	}
	if p.CreatedAt != nil {
		t := p.CreatedAt.AsTime()
		dto.CreatedAt = &t
	}
	if p.ModifiedAt != nil {
		t := p.ModifiedAt.AsTime()
		dto.ModifiedAt = &t
	}
	for _, task := range p.Tasks {
		if td := m.TaskFromProto(task); td != nil {
			dto.Tasks = append(dto.Tasks, *td)
		}
	}
	return dto
}

func (m *Mapper) TaskFromProto(p *zqntpb.TaskProtoDTO) *domains.TaskDTO {
	if p == nil {
		return nil
	}
	dto := &domains.TaskDTO{
		Status: p.Status.String(),
	}
	dto.ID = p.Id
	dto.MissionID = p.MissionId
	dto.Name = p.Name
	dto.Description = p.Description
	dto.AssetID = p.AssetId
	dto.SNNumber = p.SnNumber
	dto.CurrentProgress = p.CurrentProgress
	dto.CurrentStep = p.CurrentStep
	if p.TaskType != nil {
		s := p.GetTaskType().String()
		dto.TaskType = &s
	}
	if p.BreakReason != nil {
		s := p.GetBreakReason().String()
		dto.BreakReason = &s
	}
	if p.CreatedAt != nil {
		t := p.CreatedAt.AsTime()
		dto.CreatedAt = &t
	}
	if p.ModifiedAt != nil {
		t := p.ModifiedAt.AsTime()
		dto.ModifiedAt = &t
	}
	return dto
}

func (m *Mapper) SchedulerFromProto(p *zqntpb.SchedulerProtoDTO) *domains.SchedulerDTO {
	if p == nil {
		return nil
	}
	dto := &domains.SchedulerDTO{
		Name:           p.Name,
		CronExpression: p.CronExpression,
		Type:           p.Type.String(),
	}
	dto.ID = p.Id
	dto.MissionID = p.MissionId
	dto.TaskID = p.TaskId
	dto.Active = p.Active
	dto.ClientTimeZone = p.ClientTimeZone
	if p.CreatedAt != nil {
		t := p.CreatedAt.AsTime()
		dto.CreatedAt = &t
	}
	if p.ModifiedAt != nil {
		t := p.ModifiedAt.AsTime()
		dto.ModifiedAt = &t
	}
	return dto
}

// ---- domain → zqntpb ---------------------------------------------------------

func (m *Mapper) AssetToProto(dto *domains.AssetDTO) *zqntpb.AssetProtoDTO {
	if dto == nil {
		return nil
	}
	p := &zqntpb.AssetProtoDTO{
		Id:           dto.ID,
		Sn:           dto.SN,
		Name:         dto.Name,
		Model:        dto.Model,
		Organization: dto.Organization,
		Online:       dto.Online,
	}
	p.ConnectionString = dto.ConnectionString
	p.Port = dto.Port
	p.LiveStreamServer = dto.LiveStreamServer
	p.ExternalDeviceType = dto.ExternalDeviceType
	p.ExternalDeviceSubType = dto.ExternalDeviceSubType
	p.ExternalId = dto.ExternalID
	if dto.SubAsset != nil {
		p.SubAssetDTO = m.SubAssetToProto(dto.SubAsset)
	}
	return p
}

func (m *Mapper) SubAssetToProto(dto *domains.SubAssetDTO) *zqntpb.SubAssetProtoDTO {
	if dto == nil {
		return nil
	}
	p := &zqntpb.SubAssetProtoDTO{
		Id:           dto.ID,
		Sn:           dto.SN,
		Name:         dto.Name,
		Model:        dto.Model,
		Organization: dto.Organization,
		Online:       dto.Online,
	}
	p.ConnectionString = dto.ConnectionString
	p.Port = dto.Port
	p.LiveStreamServer = dto.LiveStreamServer
	p.ExternalDeviceType = dto.ExternalDeviceType
	p.ExternalDeviceSubType = dto.ExternalDeviceSubType
	p.ExternalId = dto.ExternalID
	p.StreamUrlPredefined = dto.StreamURLPredefined
	return p
}

func (m *Mapper) MissionToProto(dto *domains.MissionDTO) *zqntpb.MissionProtoDTO {
	if dto == nil {
		return nil
	}
	p := &zqntpb.MissionProtoDTO{
		Name:           dto.Name,
		Description:    dto.Description,
		AssignedAssets: dto.AssignedAssets,
	}
	p.Id = dto.ID
	p.GeoJson = dto.GeoJSON
	p.UpdatedUser = dto.UpdatedUser
	p.StartDate = tsPtr(dto.StartDate)
	p.EndDate = tsPtr(dto.EndDate)
	p.CreatedAt = tsPtr(dto.CreatedAt)
	p.ModifiedAt = tsPtr(dto.ModifiedAt)
	for _, task := range dto.Tasks {
		t := task
		p.Tasks = append(p.Tasks, m.TaskToProto(&t))
	}
	return p
}

func (m *Mapper) TaskToProto(dto *domains.TaskDTO) *zqntpb.TaskProtoDTO {
	if dto == nil {
		return nil
	}
	p := &zqntpb.TaskProtoDTO{}
	p.Id = dto.ID
	p.MissionId = dto.MissionID
	p.Name = dto.Name
	p.Description = dto.Description
	p.AssetId = dto.AssetID
	p.SnNumber = dto.SNNumber
	p.CurrentProgress = dto.CurrentProgress
	p.CurrentStep = dto.CurrentStep
	p.CreatedAt = tsPtr(dto.CreatedAt)
	p.ModifiedAt = tsPtr(dto.ModifiedAt)
	return p
}

func (m *Mapper) SchedulerToProto(dto *domains.SchedulerDTO) *zqntpb.SchedulerProtoDTO {
	if dto == nil {
		return nil
	}
	p := &zqntpb.SchedulerProtoDTO{
		Name:           dto.Name,
		CronExpression: dto.CronExpression,
	}
	p.Id = dto.ID
	p.MissionId = dto.MissionID
	p.TaskId = dto.TaskID
	p.Active = dto.Active
	p.ClientTimeZone = dto.ClientTimeZone
	p.CreatedAt = tsPtr(dto.CreatedAt)
	p.ModifiedAt = tsPtr(dto.ModifiedAt)
	return p
}

func tsPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
