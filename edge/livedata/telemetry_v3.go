package livedata

import (
	"context"
	"fmt"
	"time"

	"github.com/Zequent/zqnt-edge-sdk-go/v2/adapter/domains"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/protohelpers"
	"github.com/Zequent/zqnt-edge-sdk-go/v2/internal/v3compat"
	commonpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/base/proto"
	detectionpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/detection/proto"
	livedatapb "github.com/zequent/zqnt-utils-golang/v2/gen/livedata/proto"
	commonv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/common/v3"
	edgev3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/edge/v3"
	telemetryv3 "github.com/zequent/zqnt-utils-golang/v2/gen/zqnt/telemetry/v3"
	"github.com/zequent/zqnt-utils-golang/v2/telemetry"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type v3Streams struct {
	telemetry  *ingestStream[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse]
	detections *ingestStream[telemetryv3.PublishDetectionsRequest, telemetryv3.PublishDetectionsResponse]
	alerts     *ingestStream[telemetryv3.PublishAlertsRequest, telemetryv3.PublishAlertsResponse]
}

func newV3Streams(s *ServiceImpl) *v3Streams {
	if s.ingest == nil {
		return &v3Streams{}
	}
	onEnd := func(err error) {
		if v3compat.Unimplemented(err) {
			s.ingestUnavailable()
		}
	}
	return &v3Streams{
		telemetry: newIngestStream("v3 telemetry", func(ctx context.Context) (grpc.ClientStreamingClient[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse], error) {
			return s.ingest.PublishTelemetry(ctx)
		}, s.log, onEnd),
		detections: newIngestStream("v3 detections", func(ctx context.Context) (grpc.ClientStreamingClient[telemetryv3.PublishDetectionsRequest, telemetryv3.PublishDetectionsResponse], error) {
			return s.ingest.PublishDetections(ctx)
		}, s.log, onEnd),
		alerts: newIngestStream("v3 alerts", func(ctx context.Context) (grpc.ClientStreamingClient[telemetryv3.PublishAlertsRequest, telemetryv3.PublishAlertsResponse], error) {
			return s.ingest.PublishAlerts(ctx)
		}, s.log, onEnd),
	}
}

func (v *v3Streams) close() {
	if v.telemetry == nil {
		return
	}
	v.telemetry.close()
	v.detections.close()
	v.alerts.close()
}

type v2Streams struct {
	detections *ingestStream[detectionpb.DetectionBatch, livedatapb.LiveDataResponse]
}

func newV2Streams(s *ServiceImpl) *v2Streams {
	return &v2Streams{
		detections: newIngestStream("v2 detections", func(ctx context.Context) (grpc.ClientStreamingClient[detectionpb.DetectionBatch, livedatapb.LiveDataResponse], error) {
			return s.stub.ProduceDetection(ctx)
		}, s.log, nil),
	}
}

func (v *v2Streams) close() { v.detections.close() }

func (s *ServiceImpl) ingestUnavailable() {
	if s.ingestV3.MarkUnavailable() {
		s.log.Warn("platform does not serve zqnt.telemetry.v3.TelemetryIngestService; telemetry and detections go over v2, alerts are dropped",
			"retry_after", v3compat.RetryAfter)
	}
}

func (s *ServiceImpl) useV3() bool {
	return s.ingest != nil && s.ingestV3.Available()
}

// sendV3 sends over a v3 stream and reports whether the caller has to fall back to v2.
func sendV3[Req, Resp any](s *ServiceImpl, st *ingestStream[Req, Resp], req *Req) (fallback bool, err error) {
	err = st.send(req)
	if v3compat.Unimplemented(err) {
		s.ingestUnavailable()
		return true, nil
	}
	return false, err
}

// PublishTelemetrySample sends one v3 sample: the shared fields plus Details under the keys the
// adapter declares as telemetry fields. Without v3 on the platform it goes over v2 ProduceTelemetry,
// mapped like zqnt-utils' telemetry package: catalog keys land in their v2 fields, other keys are
// dropped.
func (s *ServiceImpl) PublishTelemetrySample(ctx context.Context, sample *domains.TelemetrySample) error {
	if sample == nil {
		return nil
	}
	if sample.SN == "" {
		return fmt.Errorf("livedata: telemetry sample requires SN")
	}
	if s.shuttingDown.Load() {
		return nil
	}
	req, err := telemetrySampleV3(sample)
	if err != nil {
		return err
	}
	if s.useV3() {
		fallback, err := sendV3(s, s.v3.telemetry, &telemetryv3.PublishTelemetryRequest{Sample: req})
		if !fallback {
			return err
		}
	}
	return s.ProduceTelemetry(ctx, sample.SN, telemetry.ToRequest(req))
}

// PublishDetections sends what the asset's detector saw in one frame. Without v3 on the platform
// it goes over v2 ProduceDetection.
func (s *ServiceImpl) PublishDetections(_ context.Context, batch *domains.DetectionBatch) error {
	if batch == nil {
		return nil
	}
	if batch.SN == "" {
		return fmt.Errorf("livedata: detection batch requires SN")
	}
	if s.shuttingDown.Load() {
		return nil
	}
	if s.useV3() {
		fallback, err := sendV3(s, s.v3.detections, &telemetryv3.PublishDetectionsRequest{Batch: detectionBatchV3(batch)})
		if !fallback {
			return err
		}
	}
	return s.v2.detections.send(detectionBatchV2(batch))
}

// PublishAlert sends something the asset reports on its own. v2 has no equivalent: without v3 on
// the platform the alert is dropped (logged at debug level).
func (s *ServiceImpl) PublishAlert(_ context.Context, alert *domains.Alert) error {
	if alert == nil {
		return nil
	}
	if alert.SN == "" || alert.Code == "" {
		return fmt.Errorf("livedata: alert requires SN and Code")
	}
	if s.shuttingDown.Load() {
		return nil
	}
	if s.useV3() {
		req, err := alertV3(alert)
		if err != nil {
			return err
		}
		fallback, err := sendV3(s, s.v3.alerts, &telemetryv3.PublishAlertsRequest{Alert: req})
		if !fallback {
			return err
		}
	}
	s.log.Debug("alert dropped: the platform has no v3 telemetry ingest", "sn", alert.SN, "code", alert.Code)
	return nil
}

func timestampOrNow(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return timestamppb.Now()
	}
	return timestamppb.New(t)
}

func telemetrySampleV3(in *domains.TelemetrySample) (*telemetryv3.TelemetrySample, error) {
	out := &telemetryv3.TelemetrySample{
		Asset:            &commonv3.AssetRef{Sn: in.SN},
		ObservedAt:       timestampOrNow(in.ObservedAt),
		Position:         geoPointV3(in.Position),
		RelativeAltitude: in.RelativeAltitude,
		HeadingDegrees:   in.HeadingDegrees,
		HorizontalSpeed:  in.HorizontalSpeed,
		VerticalSpeed:    in.VerticalSpeed,
		BatteryPercent:   in.BatteryPercent,
	}
	if len(in.Details) > 0 {
		details, err := structpb.NewStruct(in.Details)
		if err != nil {
			return nil, fmt.Errorf("livedata: telemetry details are not JSON-shaped: %w", err)
		}
		out.Details = details
	}
	return out, nil
}

func geoPointV3(p *domains.GeoPoint) *commonv3.GeoPoint {
	if p == nil {
		return nil
	}
	return &commonv3.GeoPoint{Latitude: p.Lat, Longitude: p.Lon, Altitude: p.Alt}
}

func detectionBatchV3(in *domains.DetectionBatch) *telemetryv3.DetectionBatch {
	out := &telemetryv3.DetectionBatch{
		Asset:      &commonv3.AssetRef{Sn: in.SN},
		ObservedAt: timestampOrNow(in.ObservedAt),
		StreamUrl:  in.StreamURL,
	}
	for _, d := range in.Detections {
		out.Detections = append(out.Detections, &edgev3.Detection{
			ObjectId:   d.ObjectID,
			ObjectType: d.ObjectType,
			Confidence: d.Confidence,
			BoundingBox: &edgev3.BoundingBox{X: d.BoundingBox.X, Y: d.BoundingBox.Y,
				Width: d.BoundingBox.Width, Height: d.BoundingBox.Height},
			Position: geoPointV3(d.Position),
		})
	}
	return out
}

func detectionBatchV2(in *domains.DetectionBatch) *detectionpb.DetectionBatch {
	out := &detectionpb.DetectionBatch{
		Base: &commonpb.RequestBase{Tid: protohelpers.GenerateTID(), Sn: in.SN, Timestamp: timestampOrNow(in.ObservedAt)},
	}
	if in.StreamURL != "" {
		out.StreamUrl = &in.StreamURL
	}
	for _, d := range in.Detections {
		out.Detections = append(out.Detections, &detectionpb.DetectionResult{
			ObjectId:   &d.ObjectID,
			ObjectType: &d.ObjectType,
			Confidence: &d.Confidence,
			BoundingBox: &detectionpb.BoundingBox{X: d.BoundingBox.X, Y: d.BoundingBox.Y,
				Width: d.BoundingBox.Width, Height: d.BoundingBox.Height},
		})
	}
	return out
}

func alertV3(in *domains.Alert) (*telemetryv3.Alert, error) {
	out := &telemetryv3.Alert{
		Asset:      &commonv3.AssetRef{Sn: in.SN},
		OccurredAt: timestampOrNow(in.OccurredAt),
		Severity:   telemetryv3.AlertSeverity(in.Severity),
		Code:       in.Code,
		Message:    in.Message,
	}
	if len(in.Details) > 0 {
		details, err := structpb.NewStruct(in.Details)
		if err != nil {
			return nil, fmt.Errorf("livedata: alert details are not JSON-shaped: %w", err)
		}
		out.Details = details
	}
	return out, nil
}
