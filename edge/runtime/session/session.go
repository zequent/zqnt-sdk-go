// Package session is the edge runtime's message layer on one link: it opens the session with a
// signed Hello (spec edge-link-handshake-v1), accepts only a Welcome signed by a platform key
// that a pinned anchor certified, answers a HelloRetry once, takes the clock offset, resume point
// and served assets from the Welcome, sends the node's queued
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
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/zequent/zqnt-api/go/v3/edgelink/handshake"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// Identity signs the Hello with the node's identity key.
type Identity interface {
	// Suite is the crypto suite id from the suite registry.
	Suite() uint32
	// KeyID is the id of the key the next Hello is signed with.
	KeyID() []byte
	// Sign signs a transcript with the key named keyID and returns r || s.
	Sign(keyID, transcript []byte) ([]byte, error)
}

// KeyConfirmer is implemented by identities that may hold a key whose rotation the platform has
// not confirmed: Confirmed reports the key of a verified Welcome, Refused a failed handshake.
type KeyConfirmer interface {
	Confirmed(keyID []byte) error
	Refused()
}

// Trust is the node's trust store.
type Trust interface {
	// OrganizationID is the node's organization; it is part of every Hello transcript.
	OrganizationID() string
	// VerifyPlatformKey checks a platform key certificate against the pinned anchors; trusted
	// says whether now comes from an authenticated platform time.
	VerifyPlatformKey(k *edgelinkv3.PlatformKey, now time.Time, trusted bool) error
	// RaiseHighWater records authenticated platform time.
	RaiseHighWater(t time.Time) error
}

// Handler receives the platform's downlink: commands, policy updates, revocations and relay
// deliveries. A frame is acknowledged once the handler returns nil and is not handed over again;
// an error leaves it unacknowledged, so the platform sends it again. The handler runs on the
// session's loop and must not block for long.
type Handler func(ctx context.Context, frame *edgelinkv3.DownlinkFrame) error

// Config configures a session.
type Config struct {
	NodeID   string
	AssetSNs []string
	// Assets, when set, is read for every Hello and replaces AssetSNs, so assets that attach while
	// the node runs are announced on the next session.
	Assets         func() []string
	RuntimeVersion string
	// LinkKind is reported in Hello, e.g. "ip-session".
	LinkKind string
	// Encodings defaults to "full".
	Encodings []string
	Store     *store.Store
	Driver    link.Driver
	Identity  Identity
	Trust     Trust
	Clock     *Clock
	Handler   Handler
	// OnHandshakeError is told why a handshake failed (a refused or invalid Welcome, a missing
	// Welcome); the session then reconnects after a backoff.
	OnHandshakeError func(err error)
	// WelcomeTimeout is how long the session waits for a valid Welcome after a Hello (15 s).
	WelcomeTimeout time.Duration
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

	hs     *pendingHello
	served []string
	keys   *handshake.SessionKeys
}

// pendingHello is the Hello the session waits to have answered on one connection.
type pendingHello struct {
	conn       uint64
	binding    []byte
	transcript []byte
	hash       []byte
	keyID      []byte
	suite      uint32
	ephemeral  *ecdh.PrivateKey
	retried    bool
	sentAt     time.Time
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
	case cfg.Identity == nil:
		return nil, errors.New("session: identity is required")
	case cfg.Trust == nil:
		return nil, errors.New("session: trust store is required")
	}
	if cfg.WelcomeTimeout <= 0 {
		cfg.WelcomeTimeout = 15 * time.Second
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

// ServedAssets are the assets the platform serves for this node in the current session: those of
// the Hello that are bound to the node.
func (s *Session) ServedAssets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.served...)
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
	defer s.lost(0)

	retry := time.NewTicker(max(s.cfg.RetransmitTimeout/2, time.Millisecond))
	defer retry.Stop()
	s.mu.Lock()
	s.ping = time.NewTicker(s.cfg.PingInterval)
	ping := s.ping
	s.mu.Unlock()
	defer ping.Stop()

	welcomeCheck := time.NewTicker(min(s.cfg.WelcomeTimeout/4, time.Second))
	defer welcomeCheck.Stop()

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
			s.receive(ctx, f)
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
				s.lost(st.Conn)
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
		case <-welcomeCheck.C:
			s.checkWelcome(ctx)
		}
		s.pump(ctx)
	}
}

// lost ends the session on connection conn; 0 ends it whatever the connection.
func (s *Session) lost(conn uint64) {
	s.mu.Lock()
	if conn != 0 && s.hs != nil && s.hs.conn != conn {
		s.mu.Unlock()
		return
	}
	refused := s.hs != nil && !s.connected
	s.connected = false
	s.hs = nil
	s.zeroKeys()
	s.mu.Unlock()
	if refused {
		s.refused()
	}
	s.cfg.Store.Release()
}

// zeroKeys overwrites the session keys; s.mu is held.
func (s *Session) zeroKeys() {
	if s.keys != nil {
		s.keys.Zero()
		s.keys = nil
	}
}

func (s *Session) refused() {
	if c, ok := s.cfg.Identity.(KeyConfirmer); ok {
		c.Refused()
	}
}

// hello builds the signed Hello that opens connection hs, with a fresh ephemeral key; nonce is
// the HelloRetry's server nonce, empty for the first Hello.
func (s *Session) hello(hs link.Handshake) ([]byte, error) {
	return s.buildHello(hs.Conn, hs.ChannelBinding, nil)
}

func (s *Session) buildHello(conn uint64, binding, nonce []byte) ([]byte, error) {
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id := s.cfg.Identity
	h := &edgelinkv3.Hello{
		CryptoSuite:    id.Suite(),
		NodeId:         s.cfg.NodeID,
		IdentityKeyId:  id.KeyID(),
		EphemeralPub:   ephemeral.PublicKey().Bytes(),
		MonotonicMs:    uint64(time.Since(s.start).Milliseconds()),
		Wallclock:      timestamppb.New(s.cfg.Clock.Local()),
		ServerNonce:    nonce,
		Encodings:      s.cfg.Encodings,
		AssetSns:       s.assets(),
		LinkKind:       s.cfg.LinkKind,
		RuntimeVersion: s.cfg.RuntimeVersion,
		DownlinkAcked:  s.cfg.Store.DownlinkAck(),
	}
	transcript := handshake.HelloTranscript(h, s.cfg.Trust.OrganizationID(), binding)
	if h.Signature, err = id.Sign(h.IdentityKeyId, transcript); err != nil {
		return nil, fmt.Errorf("sign hello: %w", err)
	}
	frame, err := proto.Marshal(&edgelinkv3.UplinkFrame{Frame: &edgelinkv3.UplinkFrame_Hello{Hello: h}})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.connected = false
	s.zeroKeys()
	s.hs = &pendingHello{
		conn:       conn,
		binding:    binding,
		transcript: transcript,
		hash:       handshake.Hash(transcript),
		keyID:      h.IdentityKeyId,
		suite:      h.CryptoSuite,
		ephemeral:  ephemeral,
		retried:    len(nonce) > 0,
		sentAt:     time.Now(),
	}
	s.mu.Unlock()
	return frame, nil
}

func (s *Session) assets() []string {
	if s.cfg.Assets != nil {
		return s.cfg.Assets()
	}
	return s.cfg.AssetSNs
}

func (s *Session) sendHello(ctx context.Context) {
	frame, err := s.hello(link.Handshake{})
	if err != nil {
		s.handshakeFailed(fmt.Errorf("session: hello: %w", err))
		return
	}
	s.cfg.Driver.Send(ctx, frame, link.SendMeta{})
}

// checkWelcome gives up on a Hello that has not been answered with a valid Welcome in time.
func (s *Session) checkWelcome(ctx context.Context) {
	s.mu.Lock()
	hs := s.hs
	late := hs != nil && !s.connected && time.Since(hs.sentAt) > s.cfg.WelcomeTimeout
	s.mu.Unlock()
	if !late {
		return
	}
	s.fail(hs, errors.New("session: no Welcome within the timeout"))
	if _, handshaking := s.cfg.Driver.(link.Handshaker); !handshaking {
		s.sendHello(ctx)
	}
}

// fail ends the handshake on hs's connection: the identity hears of the refusal and a driver
// with connections drops this one and reconnects after a backoff.
func (s *Session) fail(hs *pendingHello, err error) {
	s.mu.Lock()
	if s.hs == nil || s.hs.conn != hs.conn {
		s.mu.Unlock()
		return
	}
	s.hs, s.connected = nil, false
	s.zeroKeys()
	s.mu.Unlock()
	s.refused()
	s.handshakeFailed(err)
	if h, ok := s.cfg.Driver.(link.Handshaker); ok {
		h.Reset(hs.conn, err.Error())
	}
}

func (s *Session) handshakeFailed(err error) {
	if s.cfg.OnHandshakeError != nil {
		s.cfg.OnHandshakeError(err)
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

func (s *Session) receive(ctx context.Context, rx link.RxFrame) {
	s.mu.Lock()
	hs, connected := s.hs, s.connected
	s.mu.Unlock()
	if hs == nil || (rx.Conn != 0 && rx.Conn != hs.conn) {
		return
	}
	var f edgelinkv3.DownlinkFrame
	if err := proto.Unmarshal(rx.Payload, &f); err != nil {
		s.fail(hs, fmt.Errorf("session: undecodable frame: %w", err))
		return
	}
	if !connected {
		var err error
		switch fr := f.GetFrame().(type) {
		case *edgelinkv3.DownlinkFrame_Welcome:
			err = s.welcome(hs, fr.Welcome)
		case *edgelinkv3.DownlinkFrame_HelloRetry:
			err = s.helloRetry(ctx, hs, fr.HelloRetry)
		default:
			err = fmt.Errorf("session: %T before the Welcome", f.GetFrame())
		}
		if err != nil {
			s.fail(hs, err)
		}
		return
	}
	switch fr := f.GetFrame().(type) {
	case *edgelinkv3.DownlinkFrame_Welcome, *edgelinkv3.DownlinkFrame_HelloRetry:
		s.fail(hs, fmt.Errorf("session: %T after the Welcome", fr))
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

// platformNow is the time the platform key is checked against: authenticated platform time once
// a HelloRetry or Welcome set the clock, the unchecked local clock before.
func (s *Session) platformNow() (time.Time, bool) {
	if s.cfg.Clock.Synced() {
		return s.cfg.Clock.Now(), true
	}
	return s.cfg.Clock.Local(), false
}

// helloRetry verifies a HelloRetry, adopts its platform time and answers with a new Hello that
// carries the server nonce.
func (s *Session) helloRetry(ctx context.Context, hs *pendingHello, r *edgelinkv3.HelloRetry) error {
	if hs.retried {
		return errors.New("session: second HelloRetry")
	}
	now, trusted := s.platformNow()
	if err := VerifyHelloRetry(s.cfg.Trust, now, trusted, hs.hash, r); err != nil {
		return err
	}
	platformTime := r.GetPlatformTime().AsTime()
	s.cfg.Clock.Sync(platformTime.Sub(s.cfg.Clock.Local()))
	if err := s.cfg.Trust.RaiseHighWater(platformTime); err != nil {
		return err
	}
	frame, err := s.buildHello(hs.conn, hs.binding, r.GetServerNonce())
	if err != nil {
		return err
	}
	if !s.cfg.Driver.Send(ctx, frame, link.SendMeta{}).OK() {
		return errors.New("session: link took no Hello after the HelloRetry")
	}
	return nil
}

// welcome verifies the Welcome against the Hello it answers, derives the session keys and only
// then applies what it says.
func (s *Session) welcome(hs *pendingHello, w *edgelinkv3.Welcome) error {
	now, trusted := s.platformNow()
	keys, err := VerifyWelcome(s.cfg.Trust, now, trusted, hs.transcript, hs.ephemeral, hs.suite, w)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.hs != hs {
		s.mu.Unlock()
		keys.Zero()
		return nil
	}
	s.zeroKeys()
	s.keys = keys
	hs.ephemeral = nil
	s.mu.Unlock()

	s.cfg.Clock.Sync(time.Duration(w.GetClockOffsetMs()) * time.Millisecond)
	_ = s.cfg.Trust.RaiseHighWater(s.cfg.Clock.Now())
	if c, ok := s.cfg.Identity.(KeyConfirmer); ok {
		if err := c.Confirmed(hs.keyID); err != nil {
			s.handshakeFailed(fmt.Errorf("session: keep the confirmed identity key: %w", err))
		}
	}
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
	s.served = append([]string(nil), w.GetServedAssetSns()...)
	s.mu.Unlock()
	return nil
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
