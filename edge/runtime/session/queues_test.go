package session_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/session"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

func sampleAt(i int, at time.Time) *edgelinkv3.Envelope {
	env := sample(i)
	env.DeviceTime = timestamppb.New(at)
	return env
}

func batteries(envs []*edgelinkv3.Envelope) []int {
	var out []int
	for _, env := range envs {
		if s := env.GetSample(); s != nil {
			out = append(out, int(s.GetBatteryPercent()))
		}
	}
	return out
}

func TestFreshTelemetryGoesFirstAndTheBacklogUsesAtMostItsShare(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) {
		p.reject = true
		p.holdAcks = true
	})
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{configure: func(c *session.Config) {
		c.Window = 10
	}})
	outage := time.Now().Add(-time.Hour)
	for i := range 20 {
		publish(t, n, sampleAt(i, outage.Add(time.Duration(i)*time.Second)))
	}
	publish(t, n, sample(100), sample(101))
	p.set(func(p *platform) { p.reject = false })

	waitFor(t, "fresh samples and the backlog's share", func() bool { return received(p) == 5 })
	time.Sleep(100 * time.Millisecond)
	p.read(func(p *platform) {
		// 30 % of a window of 10: three backlog samples, newest first, after the fresh ones.
		if got := batteries(p.order); !slices.Equal(got, []int{100, 101, 19, 18, 17}) {
			t.Errorf("sent %v while unacknowledged", got)
		}
	})
	p.set(func(p *platform) { p.holdAcks = false })
	publish(t, n, sample(102))
	waitFor(t, "every sample", func() bool { return received(p) == 23 })
	waitFor(t, "an empty backlog", func() bool { return backlogEmpty(t, n.store) })
}

func TestRaisesQueueOverflowWhenAlarmsAreDropped(t *testing.T) {
	p := startPlatform(t)
	p.set(func(p *platform) { p.reject = true })
	lanes := store.DefaultLanes()
	lanes[edgelinkv3.Lane_LANE_ALARM] = store.LanePolicy{MaxItems: 3, Sync: true}
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{lanes: lanes, configure: func(c *session.Config) {
		c.OverflowAlertInterval = 200 * time.Millisecond
	}})
	for i := range 5 {
		env := alert(fmt.Sprint("dock.rain-", i))
		env.GetAlert().Severity = telemetryv3.AlertSeverity_ALERT_SEVERITY_WARNING
		publish(t, n, env)
	}
	waitFor(t, "the overflow alert queued", func() bool {
		items, err := n.store.Take(10)
		if err != nil {
			t.Fatal(err)
		}
		n.store.Release()
		return slices.ContainsFunc(items, func(it store.Item) bool {
			return it.Rank == int(telemetryv3.AlertSeverity_ALERT_SEVERITY_CRITICAL)
		})
	})
	p.set(func(p *platform) { p.reject = false })
	overflowAlerts := func() (alerts []*telemetryv3.Alert) {
		p.read(func(p *platform) {
			for _, env := range p.order {
				if a := env.GetAlert(); a.GetCode() == session.CodeQueueOverflow {
					alerts = append(alerts, a)
					if env.GetLane() != edgelinkv3.Lane_LANE_ALARM {
						t.Errorf("overflow alert in lane %v", env.GetLane())
					}
				}
			}
		})
		return alerts
	}
	waitFor(t, "every drop reported", func() bool {
		var reported float64
		for _, a := range overflowAlerts() {
			reported += a.GetDetails().GetFields()["dropped"].GetNumberValue()
		}
		return backlogEmpty(t, n.store) && reported == float64(lane(t, n.store, edgelinkv3.Lane_LANE_ALARM).Dropped)
	})
	alerts := overflowAlerts()
	if len(alerts) < 2 {
		t.Fatalf("%d overflow alerts, want the first at once and the rest after the interval", len(alerts))
	}
	first := alerts[0]
	d := first.GetDetails().GetFields()
	if first.GetSeverity() != telemetryv3.AlertSeverity_ALERT_SEVERITY_CRITICAL || d["dropped"].GetNumberValue() < 1 ||
		d["node_id"].GetStringValue() != nodeID || d["lane"].GetStringValue() != "LANE_ALARM" || d["quota_items"].GetNumberValue() != 3 {
		t.Errorf("overflow alert = %v", first)
	}
}

func TestPublishesALinkReportLatestPerLink(t *testing.T) {
	p := startPlatform(t)
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{configure: func(c *session.Config) {
		c.ReportInterval = 30 * time.Millisecond
	}})
	publish(t, n, sample(1))
	var report *edgelinkv3.LinkReport
	waitFor(t, "a link report of an UP link", func() bool {
		p.read(func(p *platform) {
			for _, env := range p.order {
				if r := env.GetLinkReport(); r != nil && r.GetLinks()[0].GetState() == edgelinkv3.LinkState_LINK_STATE_UP {
					if env.GetLane() != edgelinkv3.Lane_LANE_STATUS {
						t.Errorf("link report in lane %v", env.GetLane())
					}
					report = r
				}
			}
		})
		return report != nil
	})
	l := report.GetLinks()[0]
	if l.GetLinkId() != "lte" || l.GetDriver() != "ip-session" {
		t.Errorf("link = %v", l)
	}
	if len(report.GetBacklog()) != 5 || report.GetBacklog()[0].GetLane() != edgelinkv3.Lane_LANE_ALARM {
		t.Errorf("backlog = %v", report.GetBacklog())
	}

	direct, err := n.sess.LinkReport()
	if err != nil {
		t.Fatal(err)
	}
	if direct.GetLinks()[0].GetRttMs() == 0 && n.sess.RTT() > time.Millisecond {
		t.Errorf("rtt missing from %v", direct)
	}

	p.set(func(p *platform) { p.reject = true })
	p.killStreams()
	waitFor(t, "the session lost", func() bool { return !n.sess.Connected() })
	time.Sleep(150 * time.Millisecond)
	if b := lane(t, n.store, edgelinkv3.Lane_LANE_STATUS); b.Items != 1 {
		t.Fatalf("%d link reports queued during the outage, want the latest only", b.Items)
	}
}

func TestSeqWrapMovesToANewEpochWithoutLoss(t *testing.T) {
	p := startPlatform(t)
	n := startNode(t, filepath.Join(t.TempDir(), "node.db"), p, nodeOpts{maxSeq: 8})
	for i := range 20 {
		publish(t, n, alert(fmt.Sprint("a-", i)))
		waitFor(t, "an empty backlog", func() bool { return backlogEmpty(t, n.store) })
	}
	if n.store.Epoch() <= 7 {
		t.Fatalf("epoch = %d after 20 envelopes with seq limit 8", n.store.Epoch())
	}
	p.read(func(p *platform) {
		codes := map[string]bool{}
		epochs := map[uint32]bool{}
		for _, env := range p.order {
			codes[env.GetAlert().GetCode()] = true
			epochs[env.GetId().GetEpoch()] = true
			if env.GetId().GetSeq() > 8 {
				t.Errorf("seq %d beyond the limit", env.GetId().GetSeq())
			}
		}
		if len(codes) != 20 || len(p.order) != 20 || p.dupes != 0 {
			t.Errorf("platform got %d distinct of %d envelopes, %d dupes", len(codes), len(p.order), p.dupes)
		}
		if len(epochs) < 3 {
			t.Errorf("epochs = %v", epochs)
		}
	})
}

func lane(t *testing.T, st *store.Store, l store.Lane) store.LaneBacklog {
	t.Helper()
	backlog, err := st.Backlog()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range backlog {
		if b.Lane == l {
			return b
		}
	}
	t.Fatalf("no lane %v", l)
	return store.LaneBacklog{}
}
