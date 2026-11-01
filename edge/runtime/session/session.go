// Package session is the edge runtime's message layer on one link: it opens the session with a
// signed Hello, takes the clock offset and resume point from Welcome, sends the node's queued
// envelopes lane by lane, drops them when the platform acknowledges them, resends the rest after
// a reconnect or a retransmit timeout, and hands the platform's downlink to a handler at most
// once, acknowledging it after the handler returns.
//
// After an outage, fresh telemetry goes before the backlog, and the backlog uses at most a share
// of the link (BacklogShare). The session reports its link and the queues in a LinkReport and
// raises edge.queue_overflow when the ALARM lane had to drop alarms.
package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// Signer signs the Hello with the node's identity key.
type Signer interface {
	// Suite is the crypto suite id from the suite registry.
	Suite() uint32
	// Sign signs the deterministic encoding of the Hello with an empty signature.
	Sign(transcript []byte) ([]byte, error)
}

// Handler receives the platform's downlink: commands, policy updates, revocations and relay
// deliveries. A frame is acknowledged once the handler returns nil and is not handed over again;
// an error leaves it unacknowledged, so the platform sends it again. The handler runs on the
// session's loop and must not block for long.
type Handler func(ctx context.Context, frame *edgelinkv3.DownlinkFrame) error

// Config configures a session.
type Config struct {
	NodeID         string
	AssetSNs       []string
	RuntimeVersion string
	// LinkKind is reported in Hello, e.g. "ip-session".
	LinkKind string
	// Encodings defaults to "full".
	Encodings []string
	Store     *store.Store
	Driver    link.Driver
	// Signer is nil while nodes have no identity key; the Hello then goes out unsigned.
	Signer  Signer
	Clock   *Clock
	Handler Handler
	// Window is the most envelopes in flight (256).
	Window int
	// RetransmitTimeout is how long an envelope may stay unacknowledged before it is sent again
	// on the same session (30 s).
	RetransmitTimeout time.Duration
	// PingInterval applies until the platform's LinkPolicy sets one (15 s).
	PingInterval time.Duration
	// FreshWindow is how old a telemetry sample may be and still go before the backlog (2 min).
	FreshWindow time.Duration
	// BacklogShare is the share of the link the telemetry backlog may use (0.3): of the send
	// window, and of the driver's bandwidth estimate when it has one.
	BacklogShare float64
	// ReportInterval is how often a LinkReport goes out besides on every link state change
	// (1 min); negative turns LinkReports off.
	ReportInterval time.Duration
	// OverflowAlertInterval is the shortest gap between two edge.queue_overflow alerts (1 min).
	OverflowAlertInterval time.Duration
}

// CodeQueueOverflow is the alert code the session raises when the ALARM lane dropped alarms.
const CodeQueueOverflow = "edge.queue_overflow"

// Session is safe for concurrent use; Run drives it.
type Session struct {
	cfg   Config
	start time.Time

	mu        sync.Mutex
	connected bool
	sessionID string
	rtt       time.Duration
	nonce     uint32
	ping      *time.Ticker
	linkState link.StateChange
	policyRev string

	tokens     float64
	tokensAt   time.Time
	alarmDrops uint64
	overflowAt time.Time
}

// New checks cfg and fills in defaults.
func New(cfg Config) (*Session, error) {
	switch {
	case cfg.NodeID == "":
		return nil, errors.New("session: node id is required")
	case cfg.Store == nil:
		return nil, errors.New("session: store is required")
	case cfg.Driver == nil:
		return nil, errors.New("session: driver is required")
	}
	if len(cfg.Encodings) == 0 {
		cfg.Encodings = []string{string(link.EncodingFull)}
	}
	if cfg.Clock == nil {
		cfg.Clock = NewClock(nil)
	}
	if cfg.Window <= 0 {
		cfg.Window = 256
	}
	if cfg.RetransmitTimeout <= 0 {
		cfg.RetransmitTimeout = 30 * time.Second
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 15 * time.Second
	}
	if cfg.FreshWindow <= 0 {
		cfg.FreshWindow = 2 * time.Minute
	}
	if cfg.BacklogShare <= 0 || cfg.BacklogShare > 1 {
		cfg.BacklogShare = 0.3
	}
	if cfg.ReportInterval == 0 {
		cfg.ReportInterval = time.Minute
	}
	if cfg.OverflowAlertInterval <= 0 {
		cfg.OverflowAlertInterval = time.Minute
	}
	return &Session{cfg: cfg, start: time.Now()}, nil
}

// Clock is the session's clock.
func (s *Session) Clock() *Clock { return s.cfg.Clock }

// Connected reports whether a Welcome was received and the link has not gone down since.
func (s *Session) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// SessionID is the id of the last Welcome.
func (s *Session) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// RTT is the round-trip time of the last answered ping, 0 before the first.
func (s *Session) RTT() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rtt
}

// Publish queues an envelope for the platform and returns its id. The lane is LaneFor(env); an
// unset device time is stamped with the session clock, and envelopes stamped before the first
// clock sync are marked clock_unsynced. env is not modified.
func (s *Session) Publish(ctx context.Context, env *edgelinkv3.Envelope) (*edgelinkv3.MessageId, error) {
	env = proto.Clone(env).(*edgelinkv3.Envelope)
	env.Lane = LaneFor(env)
	env.NodeId, env.Id, env.ViaPath = "", nil, nil
	synced := s.cfg.Clock.Synced()
	if env.GetDeviceTime() == nil {
		env.DeviceTime = timestamppb.New(s.cfg.Clock.Now())
	}
	env.ClockUnsynced = env.GetClockUnsynced() || !synced
	payload, err := proto.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("session: publish: %w", err)
	}
	seq, err := s.cfg.Store.Append(ctx, store.Item{
		Lane:          env.Lane,
		Key:           keyFor(env),
		Rank:          rankFor(env),
		DeviceTime:    env.GetDeviceTime().AsTime(),
		ClockUnsynced: env.ClockUnsynced,
		Payload:       payload,
	})
	if err != nil {
		return nil, fmt.Errorf("session: publish: %w", err)
	}
	return &edgelinkv3.MessageId{Epoch: s.cfg.Store.Epoch(), Seq: seq}, nil
}

// Run opens the link and runs the session until ctx ends or the link closes. It returns ctx's
// error on cancellation.
func (s *Session) Run(ctx context.Context) error {
	d := s.cfg.Driver
	hs, handshaking := d.(link.Handshaker)
	if handshaking {
		hs.SetHandshake(s.hello)
	}
	if err := d.Open(ctx); err != nil {
		return fmt.Errorf("session: open link: %w", err)
	}
	defer func() { _ = d.Close() }()
	defer s.lost()

	retry := time.NewTicker(max(s.cfg.RetransmitTimeout/2, time.Millisecond))
	defer retry.Stop()
	s.mu.Lock()
	s.ping = time.NewTicker(s.cfg.PingInterval)
	ping := s.ping
	s.mu.Unlock()
	defer ping.Stop()

	var report <-chan time.Time
	if s.cfg.ReportInterval > 0 {
		t := time.NewTicker(s.cfg.ReportInterval)
		defer t.Stop()
		report = t.C
	}
	overflow := time.NewTimer(time.Hour)
	overflow.Stop()
	defer overflow.Stop()

	rx, states := d.Receive(), d.State()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f, ok := <-rx:
			if !ok {
				return errors.New("session: link closed")
			}
			s.receive(ctx, f.Payload)
		case st, ok := <-states:
			if !ok {
				states = nil
				continue
			}
			s.mu.Lock()
			s.linkState = st
			s.mu.Unlock()
			switch st.State {
			case link.StateDown:
				s.lost()
			case link.StateUp:
				if !handshaking {
					s.sendHello(ctx)
				}
			}
			if report != nil {
				s.publishLinkReport(ctx)
			}
		case <-report:
			s.publishLinkReport(ctx)
		case <-s.cfg.Store.Drops():
			if wait := s.checkOverflow(ctx); wait > 0 {
				overflow.Reset(wait)
			}
		case <-overflow.C:
			if wait := s.checkOverflow(ctx); wait > 0 {
				overflow.Reset(wait)
			}
		case <-s.cfg.Store.Ready():
		case <-retry.C:
			s.cfg.Store.Retry(s.cfg.RetransmitTimeout)
		case <-ping.C:
			s.sendPing(ctx)
		}
		s.pump(ctx)
	}
}

func (s *Session) lost() {
	s.mu.Lock()
	s.connected = false
	s.mu.Unlock()
	s.cfg.Store.Release()
}

func (s *Session) hello() ([]byte, error) {
	h := &edgelinkv3.Hello{
		NodeId:         s.cfg.NodeID,
		MonotonicMs:    uint64(time.Since(s.start).Milliseconds()),
		Wallclock:      timestamppb.New(s.cfg.Clock.Local()),
		Encodings:      s.cfg.Encodings,
		AssetSns:       s.cfg.AssetSNs,
		LinkKind:       s.cfg.LinkKind,
		RuntimeVersion: s.cfg.RuntimeVersion,
		DownlinkAcked:  s.cfg.Store.DownlinkAck(),
	}
	if s.cfg.Signer != nil {
		h.CryptoSuite = s.cfg.Signer.Suite()
		transcript, err := proto.MarshalOptions{Deterministic: true}.Marshal(h)
		if err != nil {
			return nil, err
		}
		if h.Signature, err = s.cfg.Signer.Sign(transcript); err != nil {
			return nil, fmt.Errorf("sign hello: %w", err)
		}
	}
	return proto.Marshal(&edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Hello{Hello: h}})
}

func (s *Session) sendHello(ctx context.Context) {
	if frame, err := s.hello(); err == nil {
		s.cfg.Driver.Send(ctx, frame, link.SendMeta{})
	}
}

func (s *Session) send(ctx context.Context, f *edgelinkv3.UplinkFrame) bool {
	b, err := proto.Marshal(f)
	if err != nil {
		return false
	}
	return s.cfg.Driver.Send(ctx, b, link.SendMeta{}).OK()
}

func (s *Session) sendPing(ctx context.Context) {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return
	}
	s.nonce++
	p := &edgelinkv3.Ping{Nonce: s.nonce, MonotonicMs: uint64(time.Since(s.start).Milliseconds())}
	s.mu.Unlock()
	s.send(ctx, &edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Ping{Ping: p}})
}

func (s *Session) receive(ctx context.Context, payload []byte) {
	var f edgelinkv3.DownlinkFrame
	if err := proto.Unmarshal(payload, &f); err != nil {
		return
	}
	switch fr := f.GetFrame().(type) {
	case *edgelinkv3.DownlinkFrame_Welcome:
		s.welcome(fr.Welcome)
	case *edgelinkv3.DownlinkFrame_Ack:
		s.cfg.Store.Ack(fr.Ack)
	case *edgelinkv3.DownlinkFrame_Pong:
		rtt := time.Since(s.start) - time.Duration(fr.Pong.GetMonotonicMs())*time.Millisecond
		s.mu.Lock()
		s.rtt = max(rtt, 0)
		s.mu.Unlock()
	case *edgelinkv3.DownlinkFrame_Policy:
		s.applyPolicy(fr.Policy.GetPolicy())
		s.deliver(ctx, &f, fr.Policy.GetId())
	case *edgelinkv3.DownlinkFrame_Command:
		s.deliver(ctx, &f, fr.Command.GetId())
	case *edgelinkv3.DownlinkFrame_Revocation:
		s.deliver(ctx, &f, fr.Revocation.GetId())
	case *edgelinkv3.DownlinkFrame_RelayDelivery:
		s.deliver(ctx, &f, fr.RelayDelivery.GetId())
	}
}

func (s *Session) welcome(w *edgelinkv3.Welcome) {
	s.cfg.Clock.Sync(time.Duration(w.GetClockOffsetMs()) * time.Millisecond)
	st := s.cfg.Store
	if r := w.GetResumeFrom(); r != nil {
		if r.GetEpoch() > st.Epoch() {
			_ = st.AdvanceEpoch(r.GetEpoch() + 1)
		} else {
			st.Ack(r)
		}
	}
	st.Release()
	s.applyPolicy(w.GetPolicy())
	s.mu.Lock()
	s.connected = true
	s.sessionID = w.GetSessionId()
	s.mu.Unlock()
}

func (s *Session) applyPolicy(p *edgelinkv3.LinkPolicy) {
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policyRev = p.GetRevision()
	if every := p.GetPingInterval().AsDuration(); every > 0 && s.ping != nil {
		s.ping.Reset(every)
	}
}

func (s *Session) deliver(ctx context.Context, f *edgelinkv3.DownlinkFrame, id *edgelinkv3.MessageId) {
	st := s.cfg.Store
	if id != nil && st.SeenDownlink(id) {
		s.ackDownlink(ctx)
		return
	}
	if s.cfg.Handler != nil {
		if err := s.cfg.Handler(ctx, f); err != nil {
			return
		}
	}
	if id == nil {
		return
	}
	if err := st.MarkDownlink(id); err != nil {
		return
	}
	s.ackDownlink(ctx)
}

func (s *Session) ackDownlink(ctx context.Context) {
	s.send(ctx, &edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Ack{Ack: s.cfg.Store.DownlinkAck()}})
}

func (s *Session) pump(ctx context.Context) {
	if !s.Connected() {
		return
	}
	st := s.cfg.Store
	for {
		room := s.cfg.Window - st.InFlight()
		if room <= 0 {
			return
		}
		items, err := st.TakeWith(s.takeOptions(min(room, 64)))
		if err != nil || len(items) == 0 {
			return
		}
		heldFrom, _ := st.HeldFrom()
		if items[0].Epoch != st.Epoch() {
			return
		}
		s.spendBacklog(items)
		for i, it := range items {
			frame, err := s.envelopeFrame(it.Epoch, min(heldFrom, it.Seq), it)
			if err == nil && s.cfg.Driver.Send(ctx, frame, link.SendMeta{}).OK() {
				continue
			}
			unsent := make([]uint32, 0, len(items)-i)
			for _, rest := range items[i:] {
				unsent = append(unsent, rest.Seq)
			}
			st.Unmark(it.Epoch, unsent...)
			return
		}
	}
}

func (s *Session) envelopeFrame(epoch, heldFrom uint32, it store.Item) ([]byte, error) {
	env := &edgelinkv3.Envelope{}
	if err := proto.Unmarshal(it.Payload, env); err != nil {
		return nil, err
	}
	env.NodeId = s.cfg.NodeID
	env.Id = &edgelinkv3.MessageId{Epoch: epoch, Seq: it.Seq}
	env.HeldFrom = heldFrom
	return proto.Marshal(&edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Envelope{Envelope: env}})
}

// takeOptions puts fresh telemetry before the backlog and caps the backlog at BacklogShare of the
// send window and, when the driver estimates its bandwidth, of that bandwidth.
func (s *Session) takeOptions(n int) store.TakeOptions {
	o := store.TakeOptions{
		Max:          n,
		FreshAfter:   s.cfg.Clock.Now().Add(-s.cfg.FreshWindow),
		BacklogItems: int(math.Ceil(s.cfg.BacklogShare*float64(s.cfg.Window))) - s.cfg.Store.InFlightBacklog(),
	}
	bps := s.cfg.Driver.Metrics().BandwidthEstimate
	if bps <= 0 || o.BacklogItems <= 0 {
		return o
	}
	rate := s.cfg.BacklogShare * float64(bps) / 8
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokensAt.IsZero() {
		s.tokens = rate
	} else {
		s.tokens = min(s.tokens+rate*now.Sub(s.tokensAt).Seconds(), rate)
	}
	s.tokensAt = now
	if s.tokens <= 0 {
		o.BacklogItems = 0
		return o
	}
	o.BacklogBytes = max(int64(s.tokens), 1)
	return o
}

// spendBacklog takes the backlog's payload from the bandwidth budget; the budget may go into
// debt by one item, which the following refill pays back.
func (s *Session) spendBacklog(items []store.Item) {
	var spent int
	for _, it := range items {
		if it.Backlog {
			spent += len(it.Payload)
		}
	}
	if spent == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tokensAt.IsZero() {
		s.tokens -= float64(spent)
	}
}

// LinkReport is the session's link and the node's queues, as published in the STATUS lane.
func (s *Session) LinkReport() (*edgelinkv3.LinkReport, error) {
	backlog, err := s.cfg.Store.Backlog()
	if err != nil {
		return nil, err
	}
	d := s.cfg.Driver
	m := d.Metrics()
	s.mu.Lock()
	st, rev, rtt := s.linkState, s.policyRev, s.rtt
	s.mu.Unlock()
	if m.RTT > 0 {
		rtt = m.RTT
	}
	status := &edgelinkv3.LinkStatus{
		LinkId:               d.ID(),
		Driver:               s.cfg.LinkKind,
		State:                edgelinkv3.LinkState(st.State),
		StateReason:          st.Reason,
		RttMs:                uint32(min(rtt.Milliseconds(), math.MaxUint32)),
		Loss:                 float32(m.Loss),
		BandwidthEstimateBps: uint64(max(m.BandwidthEstimate, 0)),
		BytesUsed:            m.BytesSent + m.BytesReceived,
		ProbeFailures:        uint32(max(m.ProbeFailures, 0)),
	}
	if !m.LastRx.IsZero() {
		status.LastRx = timestamppb.New(m.LastRx)
	}
	report := &edgelinkv3.LinkReport{Links: []*edgelinkv3.LinkStatus{status}, PolicyRevision: rev}
	for _, b := range backlog {
		lb := &edgelinkv3.LaneBacklog{Lane: b.Lane, Items: uint64(b.Items), Bytes: uint64(max(b.Bytes, 0))}
		if !b.Oldest.IsZero() {
			lb.Oldest = timestamppb.New(b.Oldest)
		}
		report.Backlog = append(report.Backlog, lb)
	}
	return report, nil
}

func (s *Session) publishLinkReport(ctx context.Context) {
	report, err := s.LinkReport()
	if err != nil {
		return
	}
	_, _ = s.Publish(ctx, &edgelinkv3.Envelope{Body: &edgelinkv3.Envelope_LinkReport{LinkReport: report}})
}

// checkOverflow raises edge.queue_overflow for alarms the ALARM lane dropped since the last
// alert. It returns how long to wait when the last alert was too recent.
func (s *Session) checkOverflow(ctx context.Context) time.Duration {
	backlog, err := s.cfg.Store.Backlog()
	if err != nil {
		return 0
	}
	var alarm store.LaneBacklog
	for _, b := range backlog {
		if b.Lane == edgelinkv3.Lane_LANE_ALARM {
			alarm = b
		}
	}
	now := time.Now()
	s.mu.Lock()
	if alarm.Dropped <= s.alarmDrops {
		s.mu.Unlock()
		return 0
	}
	if since := now.Sub(s.overflowAt); !s.overflowAt.IsZero() && since < s.cfg.OverflowAlertInterval {
		s.mu.Unlock()
		return s.cfg.OverflowAlertInterval - since
	}
	dropped := alarm.Dropped - s.alarmDrops
	s.alarmDrops, s.overflowAt = alarm.Dropped, now
	s.mu.Unlock()

	details, _ := structpb.NewStruct(map[string]any{
		"node_id":       s.cfg.NodeID,
		"lane":          edgelinkv3.Lane_LANE_ALARM.String(),
		"dropped":       float64(dropped),
		"dropped_total": float64(alarm.Dropped),
		"quota_items":   float64(alarm.MaxItems),
	})
	_, _ = s.Publish(ctx, &edgelinkv3.Envelope{
		Lane: edgelinkv3.Lane_LANE_ALARM,
		Body: &edgelinkv3.Envelope_Alert{Alert: &telemetryv3.Alert{
			OccurredAt: timestamppb.New(s.cfg.Clock.Now()),
			Severity:   telemetryv3.AlertSeverity_ALERT_SEVERITY_CRITICAL,
			Code:       CodeQueueOverflow,
			Message:    fmt.Sprintf("Edge Node %s dropped %d alarms: its alarm queue was full", s.cfg.NodeID, dropped),
			Details:    details,
		}},
	})
	return 0
}
