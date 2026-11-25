//go:build integration

package edgelink

import (
	"context"
	"testing"
	"time"
)

const (
	sampleEvery  = 50 * time.Millisecond
	baselineFor  = 10 * time.Second
	outageFor    = 30 * time.Second
	degradedFor  = 20 * time.Second
	replayingFor = 20 * time.Second
	acksHeldFor  = 50 * time.Second
	drainWithin  = 60 * time.Second
)

// TestEdgeNodeKeepsEverySampleExactlyOnce enrolls a zqnt-edge-node against edge-link through
// toxiproxy and streams telemetry through five conditions: a clean link, a 30 s outage, a slow
// lossy link, a link that keeps cutting while the hand-off behind edge-link is slow, and a link
// that holds every acknowledgement back for longer than the node's retransmit timeout, so the
// node sends envelopes the platform has already stored a second time. Every sample has to reach
// the hand-off exactly once: the node stores and forwards, edge-link acknowledges after the
// hand-off, the Welcome of a new session tells the node what already arrived, and the receive
// window drops what the node still sends again.
func TestEdgeNodeKeepsEverySampleExactlyOnce(t *testing.T) {
	requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	s := startStack(t)
	proxy := s.toxiproxy("edge-link", "0.0.0.0:8006", "edge-link:8443")
	handoff := s.toxiproxy("live-data", "0.0.0.0:8003", "live-data:8003")
	admin := s.platform()
	admin.provisionOrganization(ctx, s.organization, "Edge Link Toxiproxy Test")
	claimCode := admin.createClaim(ctx, s.organization)
	live := s.liveData()

	bin := buildNode(t)
	files := writeNodeConfig(t, s)
	enrollNode(t, bin, files, claimCode)
	node := startNode(t, bin, files)
	producer := startProducer(t, files.socket, sampleEvery)
	connected := waitFor(t, "first sample at the hand-off", time.Minute, func() bool { return live.delivered() > 0 })
	t.Logf("node started, enrolled and delivering after %s", connected.Round(time.Millisecond))

	phase := func(name string, run func() int64, within time.Duration) {
		t.Helper()
		start := time.Now()
		through := run()
		drained := waitFor(t, name+": every sample at the hand-off", within, func() bool {
			return len(live.missing(through)) == 0
		})
		if repeated := live.repeated(); len(repeated) > 0 {
			t.Fatalf("%s: samples delivered more than once: %v", name, repeated)
		}
		if node.exited() {
			t.Fatalf("%s: the node exited", name)
		}
		t.Logf("%s: %d samples produced so far, all at the hand-off exactly once, %s after the phase ended (%s in all); edge-link outcomes %v",
			name, through, drained.Round(time.Millisecond), time.Since(start).Round(time.Millisecond),
			envelopeOutcomes(t, s.ports.edgeLinkMetrics))
	}

	phase("clean link", func() int64 {
		time.Sleep(baselineFor)
		return producer.count()
	}, drainWithin)

	phase("30 s outage", func() int64 {
		before := producer.count()
		proxy.enable(false)
		time.Sleep(outageFor)
		through := producer.count()
		proxy.enable(true)
		t.Logf("30 s outage: %d samples stored while the link was down", through-before)
		return through
	}, drainWithin)

	phase("slow lossy link", func() int64 {
		proxy.addToxic("up-latency", "latency", "upstream", map[string]any{"latency": 250, "jitter": 150})
		proxy.addToxic("down-latency", "latency", "downstream", map[string]any{"latency": 250, "jitter": 150})
		proxy.addToxic("up-bandwidth", "bandwidth", "upstream", map[string]any{"rate": 32})
		proxy.addToxic("up-slicer", "slicer", "upstream", map[string]any{"average_size": 64, "size_variation": 32, "delay": 500})
		time.Sleep(degradedFor)
		through := producer.count()
		for _, toxic := range []string{"up-latency", "down-latency", "up-bandwidth", "up-slicer"} {
			proxy.removeToxic(toxic)
		}
		return through
	}, drainWithin)

	phase("replays after connection cuts", func() int64 {
		handoff.addToxic("slow-handoff", "latency", "downstream", map[string]any{"latency": 400, "jitter": 200})
		proxy.addToxic("cut", "limit_data", "upstream", map[string]any{"bytes": 24000})
		time.Sleep(replayingFor)
		through := producer.count()
		proxy.removeToxic("cut")
		handoff.removeToxic("slow-handoff")
		return through
	}, drainWithin)
	duplicatesBefore := envelopeOutcomes(t, s.ports.edgeLinkMetrics)["duplicate"]
	phase("acknowledgements held back", func() int64 {
		proxy.addToxic("hold-acks", "timeout", "downstream", map[string]any{"timeout": 0})
		time.Sleep(acksHeldFor)
		through := producer.count()
		proxy.removeToxic("hold-acks")
		return through
	}, drainWithin)
	var outcomes map[string]float64
	dropped := waitFor(t, "retransmits dropped by edge-link's receive window", 20*time.Second, func() bool {
		outcomes = envelopeOutcomes(t, s.ports.edgeLinkMetrics)
		return outcomes["duplicate"] > duplicatesBefore
	})
	t.Logf("acknowledgements held back: the receive window had dropped retransmits %s after the acknowledgements flowed again; edge-link outcomes %v",
		dropped.Round(time.Millisecond), outcomes)

	producer.stop()
	produced := producer.count()
	waitFor(t, "every sample at the hand-off", drainWithin, func() bool { return len(live.missing(produced)) == 0 })
	t.Logf("%d samples produced, %d deliveries at the hand-off, %d retransmits dropped by edge-link's receive window, %d samples delivered twice",
		produced, live.delivered(), int64(outcomes["duplicate"]-duplicatesBefore), len(live.repeated()))
	if live.delivered() != produced {
		t.Fatalf("%d deliveries at the hand-off for %d samples", live.delivered(), produced)
	}
}
