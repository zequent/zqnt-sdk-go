package session_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type msgKey struct{ epoch, seq uint32 }

// platform is an in-process EdgeLinkService that records envelopes, deduplicates them by id and
// acknowledges them the way edge-link does, settling everything below the node's low-water mark.
type platform struct {
	edgelinkv3.UnimplementedEdgeLinkServiceServer
	addr string

	mu        sync.Mutex
	reject    bool
	holdAcks  bool
	blackhole bool
	offsetMs  int64
	epoch     uint32
	low       uint32
	got       map[msgKey]*edgelinkv3.Envelope
	order     []*edgelinkv3.Envelope
	frames    int
	dupes     int
	hellos    []*edgelinkv3.Hello
	dlAcks    []*edgelinkv3.Ack
	sessions  int
	kills     []context.CancelFunc
	sendQ     chan *edgelinkv3.DownlinkFrame
}

func startPlatform(t *testing.T) *platform {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &platform{
		addr:  lis.Addr().String(),
		got:   map[msgKey]*edgelinkv3.Envelope{},
		sendQ: make(chan *edgelinkv3.DownlinkFrame, 64),
	}
	srv := grpc.NewServer()
	edgelinkv3.RegisterEdgeLinkServiceServer(srv, p)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return p
}

func (p *platform) set(f func(p *platform)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

func (p *platform) read(f func(p *platform)) { p.set(f) }

func (p *platform) killStreams() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, kill := range p.kills {
		kill()
	}
	p.kills = nil
}

func (p *platform) ack() *edgelinkv3.Ack {
	a := &edgelinkv3.Ack{Epoch: p.epoch, CumulativeSeq: p.low}
	for {
		if _, ok := p.got[msgKey{p.epoch, a.CumulativeSeq + 1}]; !ok {
			break
		}
		a.CumulativeSeq++
	}
	for k := range p.got {
		if k.epoch != p.epoch || k.seq <= a.CumulativeSeq {
			continue
		}
		i := k.seq - a.CumulativeSeq - 1
		for uint32(len(a.SelectiveBitmap)) <= i/8 {
			a.SelectiveBitmap = append(a.SelectiveBitmap, 0)
		}
		a.SelectiveBitmap[i/8] |= 1 << (i % 8)
	}
	return a
}

func (p *platform) Connect(stream edgelinkv3.EdgeLinkService_ConnectServer) error {
	p.mu.Lock()
	reject := p.reject
	p.mu.Unlock()
	if reject {
		return status.Error(codes.Unavailable, "edge-link not ready")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
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
	p.hellos = append(p.hellos, proto.Clone(hello).(*edgelinkv3.Hello))
	p.sessions++
	p.kills = append(p.kills, cancel)
	welcome := &edgelinkv3.Welcome{ClockOffsetMs: p.offsetMs, ResumeFrom: p.ack(), SessionId: fmt.Sprint("s", p.sessions)}
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
		select {
		case <-ctx.Done():
			return status.Error(codes.Unavailable, "stream closed")
		case f := <-frames:
			if a := p.handle(f); a != nil {
				if err := send(&edgelinkv3.DownlinkFrame{Frame: &edgelinkv3.DownlinkFrame_Ack{Ack: a}}); err != nil {
					return err
				}
			}
		}
	}
}

func (p *platform) handle(f *edgelinkv3.UplinkFrame) *edgelinkv3.Ack {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch fr := f.GetFrame().(type) {
	case *edgelinkv3.UplinkFrame_Ack:
		p.dlAcks = append(p.dlAcks, fr.Ack)
	case *edgelinkv3.UplinkFrame_Envelope:
		p.frames++
		if p.blackhole {
			return nil
		}
		env := fr.Envelope
		k := msgKey{env.GetId().GetEpoch(), env.GetId().GetSeq()}
		if k.epoch > p.epoch {
			p.epoch, p.low = k.epoch, 0
		}
		if k.epoch == p.epoch && env.GetHeldFrom() > p.low+1 {
			p.low = env.GetHeldFrom() - 1
		}
		if _, dup := p.got[k]; dup {
			p.dupes++
		} else {
			p.got[k] = env
			p.order = append(p.order, env)
		}
		if !p.holdAcks {
			return p.ack()
		}
	}
	return nil
}
