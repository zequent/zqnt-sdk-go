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
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	edgesdk "github.com/zequent/zqnt-sdk-go/v3/edge"
	sdkadapter "github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter/domains"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/node"
)

// platform is an in-process EdgeLinkService: it welcomes every session, records envelopes and
// Hellos, acknowledges what it holds, and sends the downlink frames queued on it.
type platform struct {
	edgelinkv3.UnimplementedEdgeLinkServiceServer
	addr string

	mu     sync.Mutex
	epoch  uint32
	got    map[uint32]*edgelinkv3.Envelope
	order  []*edgelinkv3.Envelope
	hellos []*edgelinkv3.Hello
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
	p := &platform{addr: lis.Addr().String(), got: map[uint32]*edgelinkv3.Envelope{}, sendQ: make(chan *edgelinkv3.DownlinkFrame, 16)}
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
	if first.GetHello() == nil {
		return status.Error(codes.InvalidArgument, "first frame must be Hello")
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	var sendMu sync.Mutex
	send := func(f *edgelinkv3.DownlinkFrame) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(f)
	}
	p.mu.Lock()
	p.hellos = append(p.hellos, proto.Clone(first.GetHello()).(*edgelinkv3.Hello))
	p.kills = append(p.kills, cancel)
	welcome := &edgelinkv3.Welcome{SessionId: fmt.Sprint("s", len(p.hellos)), ResumeFrom: p.ackLocked()}
	p.mu.Unlock()
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
node_id: pi-1
assets: [STATIC-1]
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
`, filepath.Join(t.TempDir(), "node.db"), nodeSock, adapterSock, p.addr))
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
				return env.GetLane() == edgelinkv3.Lane_LANE_TELEMETRY && env.GetNodeId() == "pi-1"
			}
		}
		return false
	})

	if got := p.helloAssets()[0]; !slices.Equal(got, []string{"STATIC-1"}) {
		t.Fatalf("first Hello announced %v", got)
	}
	p.killStreams()
	waitFor(t, "a Hello with the attached asset", func() bool {
		hellos := p.helloAssets()
		return len(hellos) > 1 && slices.Equal(hellos[len(hellos)-1], []string{asset, "STATIC-1"})
	})
	waitFor(t, "the session back", n.Connected)

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
	for _, want := range []string{"edge node started", "connected to the platform", "edge node stopped", "Hello goes out unsigned"} {
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
	cfg, err := node.ParseConfig(fmt.Appendf(nil, `
node_id: pi-1
store: {path: %q}
local: {socket: %q}
links: [{id: lan, driver: ip-session, endpoint: "127.0.0.1:1", insecure: true}]
`, filepath.Join(dir, "node.db"), blocker))
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
