package session

import (
	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
)

// LaneFor is the lane an envelope travels in when it names none: alerts of WARNING and above
// are ALARM, other alerts and detections BULK, command events COMMAND_REPLY, capability, link and
// topology reports STATUS, samples TELEMETRY. Alarm detections must name ALARM themselves.
func LaneFor(env *edgelinkv3.Envelope) edgelinkv3.Lane {
	if env.GetLane() != edgelinkv3.Lane_LANE_UNSPECIFIED {
		return env.GetLane()
	}
	switch b := env.GetBody().(type) {
	case *edgelinkv3.Envelope_Alert:
		if b.Alert.GetSeverity() >= telemetryv3.AlertSeverity_ALERT_SEVERITY_WARNING {
			return edgelinkv3.Lane_LANE_ALARM
		}
		return edgelinkv3.Lane_LANE_BULK
	case *edgelinkv3.Envelope_CommandEvent:
		return edgelinkv3.Lane_LANE_COMMAND_REPLY
	case *edgelinkv3.Envelope_Capabilities, *edgelinkv3.Envelope_LinkReport, *edgelinkv3.Envelope_Topology:
		return edgelinkv3.Lane_LANE_STATUS
	case *edgelinkv3.Envelope_Sample:
		return edgelinkv3.Lane_LANE_TELEMETRY
	}
	return edgelinkv3.Lane_LANE_BULK
}

// keyFor names what a STATUS envelope reports, so a newer report replaces a queued older one.
func keyFor(env *edgelinkv3.Envelope) string {
	switch b := env.GetBody().(type) {
	case *edgelinkv3.Envelope_Capabilities:
		return "capabilities/" + b.Capabilities.GetAssetSn()
	case *edgelinkv3.Envelope_LinkReport:
		key := "link_report"
		for _, l := range b.LinkReport.GetLinks() {
			key += "/" + l.GetLinkId()
		}
		return key
	case *edgelinkv3.Envelope_Topology:
		return "topology"
	}
	return ""
}

// rankFor orders eviction in a full lane: info before warnings before critical alerts, progress
// events before terminal ones.
func rankFor(env *edgelinkv3.Envelope) int {
	switch b := env.GetBody().(type) {
	case *edgelinkv3.Envelope_Alert:
		return int(b.Alert.GetSeverity())
	case *edgelinkv3.Envelope_CommandEvent:
		switch b.CommandEvent.GetState() {
		case capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED,
			capabilityv3.CommandState_COMMAND_STATE_FAILED,
			capabilityv3.CommandState_COMMAND_STATE_REJECTED,
			capabilityv3.CommandState_COMMAND_STATE_CANCELLED,
			capabilityv3.CommandState_COMMAND_STATE_TIMED_OUT:
			return 1
		}
	}
	return 0
}
