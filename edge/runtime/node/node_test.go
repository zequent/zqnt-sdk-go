package node_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capabilityv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/capability/v3"
	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	edgesdk "github.com/zequent/zqnt-sdk-go/v3/edge"
	sdkadapter "github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter/domains"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity/identitytest"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/node"
)

// platform is an in-process EdgeLinkService: it checks every Hello against the enrolled node's
// key, sends a signed Welcome that serves the announced assets but UNBOUND-*, records envelopes,
// Hellos and credentials, acknowledges what it holds, and sends the downlink frames queued on it.
type platform struct {
	edgelinkv3.UnimplementedEdgeLinkServiceServer
	addr     string
	auth     *identitytest.Authority
	org      string
	enrolled *identitytest.Enrolled

	mu     sync.Mutex
	epoch  uint32
	got    map[uint32]*edgelinkv3.Envelope
	order  []*edgelinkv3.Envelope
	hellos []*edgelinkv3.Hello
	tokens []string
	dlAcks []*edgelinkv3.Ack
	kills  []context.CancelFunc
	sendQ  chan *edgelinkv3.DownlinkFrame
}

func startPlatform(t *testing.T) *platform {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	auth := identitytest.NewAuthority(t)
	org := "6f1c2a0e-3b5d-4c8e-9a7f-1d2e3f405162"
	p := &platform{
		addr: lis.Addr().String(), auth: auth, org: org, enrolled: identitytest.Enroll(t, auth, org),
		got: map[uint32]*edgelinkv3.Envelope{}, sendQ: make(chan *edgelinkv3.DownlinkFrame, 16),
	}
	srv := grpc.NewServer()
	edgelinkv3.RegisterEdgeLinkServiceServer(srv, p)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return p
}

func (p *platform) ackLocked() *edgelinkv3.Ack {
	a := &edgelinkv3.Ack{Epoch: p.epoch}
	for p.got[a.CumulativeSeq+1] != nil {
		a.CumulativeSeq++
	}
	return a
}

func (p *platform) Connect(stream edgelinkv3.EdgeLinkService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first frame must be Hello")
	}
	transcript, err := identitytest.VerifyHello(hello, p.org, nil, p.enrolled.Key.Public)
	if err != nil || hello.GetNodeId() != p.enrolled.State.NodeID {
		return status.Error(codes.Unauthenticated, "handshake refused")
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	var sendMu sync.Mutex
	send := func(f *edgelinkv3.DownlinkFrame) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(f)
	}
	p.mu.Lock()
	p.hellos = append(p.hellos, proto.Clone(hello).(*edgelinkv3.Hello))
	p.tokens = append(p.tokens, md.Get("authorization")...)
	p.kills = append(p.kills, cancel)
	welcome := &edgelinkv3.Welcome{SessionId: fmt.Sprint("s", len(p.hellos)), ResumeFrom: p.ackLocked()}
	for _, sn := range hello.GetAssetSns() {
		if !strings.HasPrefix(sn, "UNBOUND-") {
			welcome.ServedAssetSns = append(welcome.ServedAssetSns, sn)
		}
	}
	p.mu.Unlock()
	p.auth.Welcome(transcript, hello, welcome).Zero()
	if err := send(&edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Welcome{Welcome: welcome}}); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-p.sendQ:
				if send(f) != nil {
					return
				}
			}
		}
	}()
	frames := make(chan *edgelinkv3.UplinkFrame)
	go func() {
		defer cancel()
		for {
			f, err := stream.Recv()
			if err != nil {
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		var f *edgelinkv3.UplinkFrame
		select {
		case <-ctx.Done():
			return status.Error(codes.Unavailable, "stream closed")
		case f = <-frames:
		}
		p.mu.Lock()
		var ack *edgelinkv3.Ack
		switch fr := f.GetFrame().(type) {
		case *edgelinkv3.UplinkFrame_Ack:
			p.dlAcks = append(p.dlAcks, fr.Ack)
		case *edgelinkv3.UplinkFrame_Envelope:
			env := fr.Envelope
			if e := env.GetId().GetEpoch(); e != p.epoch {
				p.epoch, p.got = e, map[uint32]*edgelinkv3.Envelope{}
			}
			if p.got[env.GetId().GetSeq()] == nil {
				p.got[env.GetId().GetSeq()] = env
				p.order = append(p.order, env)
			}
			ack = p.ackLocked()
		}
		p.mu.Unlock()
		if ack != nil {
			if err := send(&edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Ack{Ack: ack}}); err != nil {
				return err
			}
		}
	}
}

func (p *platform) killStreams() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, kill := range p.kills {
		kill()
	}
	p.kills = nil
}

func (p *platform) envelopes() []*edgelinkv3.Envelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.order)
}

func (p *platform) helloAssets() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out [][]string
	for _, h := range p.hellos {
		out = append(out, h.GetAssetSns())
	}
	return out
}

func (p *platform) commandStates(id string) []capabilityv3.CommandState {
	var out []capabilityv3.CommandState
	for _, env := range p.envelopes() {
		if ev := env.GetCommandEvent(); ev.GetCommandExecutionId() == id {
			out = append(out, ev.GetState())
		}
	}
	return out
}

func (p *platform) downlinkAcked(seq uint32) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.dlAcks {
		if a.GetCumulativeSeq() >= seq {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

const asset = "DOCK-1"

// The node runs in-process between a platform and an adapter built with the Go SDK: the adapter's
// capabilities and telemetry reach the platform, the asset it registered is announced in the
// next Hello, a delivered command runs once on the adapter and its outcome goes back, and SIGTERM
// (ctx) shuts the node down cleanly.
func TestTheNodeCarriesAnSDKAdapterToThePlatform(t *testing.T) {
	p := startPlatform(t)
	dir, err := os.MkdirTemp("", "zn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	nodeSock, adapterSock := filepath.Join(dir, "node.sock"), filepath.Join(dir, "adapter.sock")

	cfg, err := node.ParseConfig(fmt.Appendf(nil, `
identity: {key_store: software-dev, dev: true, dir: %q}
assets: [STATIC-1, UNBOUND-1]
store: {path: %q, flush_interval: 10ms}
local:
  socket: %q
  default_adapter_endpoint: unix://%s
report_interval: 1s
shutdown_timeout: 2s
links:
  - id: lan
    driver: ip-session
    endpoint: %q
    insecure: true
    min_backoff: 20ms
    max_backoff: 100ms
`, p.enrolled.Dir, filepath.Join(t.TempDir(), "node.db"), nodeSock, adapterSock, p.addr))
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	n, err := node.New(node.Options{
		Config: cfg, Version: "test", StatusInterval: 10 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.SocketPath() != nodeSock {
		t.Fatalf("socket path %q", n.SocketPath())
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	runDone := make(chan error, 1)
	go func() { runDone <- n.Run(ctx) }()
	waitFor(t, "the first session", n.Connected)

	var takeoffs atomic.Int32
	dock := &struct{ sdkadapter.Base }{}
	dock.MustRegisterCommand("flight.takeoff", nil, nil, func(context.Context, *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		takeoffs.Add(1)
		return domains.Success("airborne", asset), nil
	})
	client, err := edgesdk.NewEdgeClient("unix://"+nodeSock, asset, dock, edgesdk.WithoutPlatformAuth(),
		edgesdk.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	adapterLis, err := local.Listen(adapterSock, local.ListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	adapterCtx, stopAdapter := context.WithCancel(context.Background())
	defer stopAdapter()
	go func() { _ = client.StartServing(adapterCtx, adapterLis) }()
	defer func() { _ = client.Shutdown(context.Background()) }()

	waitFor(t, "the adapter's capabilities at the platform", func() bool {
		for _, env := range p.envelopes() {
			if env.GetCapabilities().GetAssetSn() == asset {
				return true
			}
		}
		return false
	})

	waitFor(t, "telemetry at the platform", func() bool {
		_ = client.LiveData().PublishTelemetrySample(context.Background(), &domains.TelemetrySample{
			SN: asset, ObservedAt: time.Now(), Details: map[string]any{"dock.mode": "idle"},
		})
		for _, env := range p.envelopes() {
			if s := env.GetSample(); s.GetAsset().GetSn() == asset {
				return env.GetLane() == edgelinkv3.Lane_LANE_TELEMETRY && env.GetNodeId() == n.NodeID()
			}
		}
		return false
	})

	if got := p.helloAssets()[0]; !slices.Equal(got, []string{"STATIC-1", "UNBOUND-1"}) {
		t.Fatalf("first Hello announced %v", got)
	}
	p.mu.Lock()
	if p.tokens[0] != "Bearer test-credential" {
		t.Errorf("credential %q, want the enrolled one", p.tokens[0])
	}
	p.mu.Unlock()
	p.killStreams()
	waitFor(t, "a Hello with the attached asset", func() bool {
		hellos := p.helloAssets()
		return len(hellos) > 1 && slices.Equal(hellos[len(hellos)-1], []string{asset, "STATIC-1", "UNBOUND-1"})
	})
	waitFor(t, "the session back", n.Connected)
	if served := n.ServedAssets(); !slices.Equal(served, []string{asset, "STATIC-1"}) {
		t.Fatalf("served assets %v", served)
	}

	delivery := &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Command{Command: &edgelinkv3.CommandDelivery{
		Id:                 &edgelinkv3.MessageId{Epoch: 1, Seq: 1},
		Context:            &commonv3.RequestContext{RequestId: "req-1"},
		CommandExecutionId: "c-1",
		Action: &edgelinkv3.CommandDelivery_Execute{Execute: &capabilityv3.Command{
			Asset: &commonv3.AssetRef{Sn: asset}, CommandId: "flight.takeoff",
		}},
	}}}
	p.sendQ <- delivery
	waitFor(t, "the command outcome at the platform", func() bool { return len(p.commandStates("c-1")) == 2 })
	if got := p.commandStates("c-1"); !slices.Equal(got, []capabilityv3.CommandState{
		capabilityv3.CommandState_COMMAND_STATE_ACCEPTED, capabilityv3.CommandState_COMMAND_STATE_SUCCEEDED,
	}) {
		t.Fatalf("states %v", got)
	}
	waitFor(t, "the delivery acknowledged", func() bool { return p.downlinkAcked(1) })

	p.sendQ <- delivery
	waitFor(t, "the repeated delivery acknowledged", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.dlAcks) >= 2
	})
	if takeoffs.Load() != 1 {
		t.Fatalf("takeoff ran %d times", takeoffs.Load())
	}

	waitFor(t, "a stats line", func() bool { return strings.Contains(logs.String(), "node stats") })

	stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the node did not stop")
	}
	if _, err := os.Lstat(nodeSock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the node socket is still there: %v", err)
	}
	for _, want := range []string{"edge node started", "connected to the platform", "edge node stopped", "development node",
		"the platform does not serve these assets", "UNBOUND-1"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs.String())
		}
	}
}

func TestANodeThatCannotStartReleasesItsStore(t *testing.T) {
	dir, err := os.MkdirTemp("", "zn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	blocker := filepath.Join(dir, "node.sock")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	enrolled := identitytest.Enroll(t, identitytest.NewAuthority(t), "6f1c2a0e-3b5d-4c8e-9a7f-1d2e3f405162")
	cfg, err := node.ParseConfig(fmt.Appendf(nil, `
identity: {key_store: software-dev, dev: true, dir: %q}
store: {path: %q}
local: {socket: %q}
links: [{id: lan, driver: ip-session, endpoint: "127.0.0.1:1", insecure: true}]
`, enrolled.Dir, filepath.Join(dir, "node.db"), blocker))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.New(node.Options{Config: cfg}); err == nil || !strings.Contains(err.Error(), "is not a socket") {
		t.Fatalf("err %v", err)
	}
	cfg.Local.Socket = filepath.Join(dir, "ok.sock")
	n, err := node.New(node.Options{Config: cfg})
	if err != nil {
		t.Fatalf("store still held: %v", err)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestANodeNeedsAnEnrollmentThatFitsItsConfig(t *testing.T) {
	enrolled := identitytest.Enroll(t, identitytest.NewAuthority(t), "6f1c2a0e-3b5d-4c8e-9a7f-1d2e3f405162")
	base := `
store: {path: %q}
local: {socket: %q}
links: [{id: lan, driver: ip-session, endpoint: "127.0.0.1:1", insecure: true}]
`
	for name, c := range map[string]struct{ identity, want string }{
		"not enrolled":    {fmt.Sprintf("{key_store: software-dev, dev: true, dir: %q}", t.TempDir()), "not enrolled"},
		"other class":     {fmt.Sprintf("{key_store: software-dev, dev: true, dir: %q, node_class: personal}", enrolled.Dir), "enrolled as a infrastructure node"},
		"other suite":     {fmt.Sprintf("{key_store: software-dev, dev: true, dir: %q, suite: s1-aes-256-gcm}", enrolled.Dir), "enrolled with suite"},
		"other key store": {fmt.Sprintf("{key_store: software-sealed, sealing_key_env: ZQNT_TEST_SEALING_KEY, dir: %q}", enrolled.Dir), "holds no identity key"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ZQNT_TEST_SEALING_KEY", strings.Repeat("42", 32))
			dir, err := os.MkdirTemp("", "zn")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			cfg, err := node.ParseConfig(fmt.Appendf(nil, "identity: "+c.identity+base, filepath.Join(dir, "node.db"), filepath.Join(dir, "n.sock")))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := node.New(node.Options{Config: cfg}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestARevokedNodeErasesItsIdentityAndStops(t *testing.T) {
	p := startPlatform(t)
	dir, err := os.MkdirTemp("", "zn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg, err := node.ParseConfig(fmt.Appendf(nil, `
identity: {key_store: software-dev, dev: true, dir: %q}
store: {path: %q, flush_interval: 10ms}
local: {socket: %q}
links: [{id: lan, driver: ip-session, endpoint: %q, insecure: true, min_backoff: 20ms, max_backoff: 100ms}]
`, p.enrolled.Dir, filepath.Join(dir, "node.db"), filepath.Join(dir, "n.sock"), p.addr))
	if err != nil {
		t.Fatal(err)
	}
	n, err := node.New(node.Options{Config: cfg, StatusInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- n.Run(context.Background()) }()
	waitFor(t, "the session", n.Connected)
	p.sendQ <- &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Revocation{Revocation: &edgelinkv3.Revocation{
		Id: &edgelinkv3.MessageId{Epoch: 1, Seq: 1}, NodeIds: []string{"other-node"}, Reason: "lost",
	}}}
	waitFor(t, "the revocation of another node acknowledged", func() bool { return p.downlinkAcked(1) })
	p.sendQ <- &edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Revocation{Revocation: &edgelinkv3.Revocation{
		Id: &edgelinkv3.MessageId{Epoch: 1, Seq: 2}, NodeIds: []string{n.NodeID()}, Reason: "reported lost",
	}}}
	select {
	case err := <-runDone:
		if err == nil || !strings.Contains(err.Error(), "revoked") {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a revoked node kept running")
	}
	if _, err := p.enrolled.Dir.LoadState(); !errors.Is(err, identity.ErrNotEnrolled) {
		t.Fatalf("enrollment after revocation: %v", err)
	}
	if _, _, err := p.enrolled.Dir.Credential(); err == nil {
		t.Fatal("credential kept after revocation")
	}
	if _, err := p.enrolled.Store.Load(identity.KeyActive); !errors.Is(err, identity.ErrNoKey) {
		t.Fatalf("identity key kept after revocation: %v", err)
	}
}
