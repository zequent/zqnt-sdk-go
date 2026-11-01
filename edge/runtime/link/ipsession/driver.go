// Package ipsession is the link driver for any TCP/IP link (Ethernet, Wi-Fi, LTE/5G, MANET
// radios): one EdgeLinkService.Connect stream the node dials out over TLS, re-opened with
// backoff and jitter whenever it breaks. Frames are serialized UplinkFrame and DownlinkFrame
// messages; the driver passes them through without decoding.
package ipsession

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/mem"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
)

// DriverID is the id the driver registers under.
const DriverID = "ip-session"

const (
	maxFrame   = 4 << 20
	sendBuffer = 1024
	rxBuffer   = 1024
)

func init() {
	link.Register(DriverID, func(cfg link.LinkConfig) (link.Driver, error) {
		opts, err := optionsFrom(cfg)
		if err != nil {
			return nil, err
		}
		return New(cfg.ID, opts)
	})
}

// Options configure the driver.
type Options struct {
	// Endpoint is the platform's edge-link address, host:port.
	Endpoint string
	// Insecure dials without TLS; for tests and local stacks only.
	Insecure bool
	// CAFile holds PEM roots for the platform certificate; empty uses the system roots.
	CAFile     string
	ServerName string
	// TokenFile holds the node credential, read again for every connection.
	TokenFile  string
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// KeepaliveTime is how long the connection may be idle before the driver pings it.
	KeepaliveTime time.Duration
	DialOptions   []grpc.DialOption
}

func optionsFrom(cfg link.LinkConfig) (Options, error) {
	var o Options
	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	var err error
	o.Endpoint, err = cfg.RequiredString("endpoint")
	collect(err)
	o.Insecure, err = cfg.Bool("insecure", false)
	collect(err)
	o.CAFile, err = cfg.String("ca_file", "")
	collect(err)
	o.ServerName, err = cfg.String("server_name", "")
	collect(err)
	o.TokenFile, err = cfg.String("token_file", "")
	collect(err)
	o.MinBackoff, err = cfg.Duration("min_backoff", 0)
	collect(err)
	o.MaxBackoff, err = cfg.Duration("max_backoff", 0)
	collect(err)
	o.KeepaliveTime, err = cfg.Duration("keepalive", 0)
	collect(err)
	return o, errors.Join(errs...)
}

// Driver is safe for concurrent use.
type Driver struct {
	id       string
	opts     Options
	dialOpts []grpc.DialOption
	rx       chan link.RxFrame
	state    *link.StateFeed

	mu        sync.Mutex
	handshake func() ([]byte, error)
	conn      *grpc.ClientConn
	out       chan []byte
	opened    bool
	closed    bool
	cancel    context.CancelFunc
	done      chan struct{}
	metrics   link.Metrics
}

// New builds an unopened driver for link id.
func New(id string, opts Options) (*Driver, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 500 * time.Millisecond
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 8 * time.Second
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = opts.MinBackoff
	}
	if opts.KeepaliveTime <= 0 {
		opts.KeepaliveTime = 20 * time.Second
	}
	dialOpts, err := dialOptions(opts)
	if err != nil {
		return nil, err
	}
	return &Driver{
		id:       id,
		opts:     opts,
		dialOpts: dialOpts,
		rx:       make(chan link.RxFrame, rxBuffer),
		state:    link.NewStateFeed(link.StateDown, "not opened"),
		done:     make(chan struct{}),
	}, nil
}

func dialOptions(o Options) ([]grpc.DialOption, error) {
	opts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: o.KeepaliveTime, Timeout: 10 * time.Second}),
	}
	if o.Insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: o.ServerName}
		if o.CAFile != "" {
			pem, err := os.ReadFile(o.CAFile)
			if err != nil {
				return nil, fmt.Errorf("ca_file: %w", err)
			}
			cfg.RootCAs = x509.NewCertPool()
			if !cfg.RootCAs.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("ca_file %s: no certificates", o.CAFile)
			}
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	}
	if o.TokenFile != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(tokenFile{path: o.TokenFile, secure: !o.Insecure}))
	}
	return append(opts, o.DialOptions...), nil
}

type tokenFile struct {
	path   string
	secure bool
}

func (t tokenFile) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	b, err := os.ReadFile(t.path)
	if err != nil {
		return nil, fmt.Errorf("token_file: %w", err)
	}
	return map[string]string{"authorization": "Bearer " + strings.TrimSpace(string(b))}, nil
}

func (t tokenFile) RequireTransportSecurity() bool { return t.secure }

// SetHandshake sets the frame that opens every connection (the session's Hello).
func (d *Driver) SetHandshake(handshake func() ([]byte, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handshake = handshake
}

func (d *Driver) ID() string { return d.id }

func (d *Driver) Caps() link.Caps {
	security := link.SecurityLinkAuth
	if d.opts.Insecure {
		security = link.SecurityNone
	}
	return link.Caps{
		MaxPayload:    maxFrame,
		Streaming:     true,
		Bidirectional: true,
		Encodings:     []link.Encoding{link.EncodingFull},
		Security:      security,
	}
}

func (d *Driver) Open(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case d.closed:
		return errors.New("ipsession: driver closed")
	case d.opened:
		return errors.New("ipsession: driver already open")
	}
	conn, err := grpc.NewClient(d.opts.Endpoint, d.dialOpts...)
	if err != nil {
		return fmt.Errorf("ipsession: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.conn, d.cancel, d.opened = conn, cancel, true
	d.state.Publish(link.StateDown, "connecting")
	go d.run(ctx)
	return nil
}

func (d *Driver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	opened, conn, cancel := d.opened, d.conn, d.cancel
	d.mu.Unlock()
	var err error
	if opened {
		cancel()
		<-d.done
		err = conn.Close()
	}
	d.state.Close()
	close(d.rx)
	return err
}

func (d *Driver) Send(_ context.Context, frame []byte, _ link.SendMeta) link.SendResult {
	if len(frame) > maxFrame {
		return link.SendResult{Status: link.TooLarge, Reason: fmt.Sprintf("%d bytes, at most %d", len(frame), maxFrame)}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.out == nil {
		return link.SendResult{Status: link.Down, Reason: "not connected"}
	}
	select {
	case d.out <- append([]byte(nil), frame...):
		return link.SendResult{Status: link.Queued}
	default:
		return link.SendResult{Status: link.Down, Reason: "send buffer full"}
	}
}

func (d *Driver) Receive() <-chan link.RxFrame { return d.rx }

func (d *Driver) State() <-chan link.StateChange { return d.state.C() }

func (d *Driver) Metrics() link.Metrics {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.metrics
}

func (d *Driver) MeshInfo() link.MeshInfo { return link.MeshInfo{Kind: link.MeshNone} }

func (d *Driver) Neighbors() []link.Neighbor { return nil }

func (d *Driver) run(ctx context.Context) {
	defer close(d.done)
	backoff := d.opts.MinBackoff
	for {
		established, err := d.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		reason := "stream ended"
		if err != nil {
			reason = err.Error()
		}
		d.state.Publish(link.StateDown, reason)
		d.mu.Lock()
		d.metrics.ProbeFailures++
		d.mu.Unlock()
		if established {
			backoff = d.opts.MinBackoff
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		backoff = min(backoff*2, d.opts.MaxBackoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// connect runs one Connect stream until it breaks; established is true once the platform sent
// anything on it.
func (d *Driver) connect(ctx context.Context) (established bool, err error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := d.conn.NewStream(sctx, &edgelinkv3.EdgeLinkService_ServiceDesc.Streams[0],
		edgelinkv3.EdgeLinkService_Connect_FullMethodName, grpc.ForceCodecV2(rawCodec{}))
	if err != nil {
		return false, err
	}
	d.mu.Lock()
	handshake := d.handshake
	d.mu.Unlock()
	if handshake != nil {
		hello, err := handshake()
		if err != nil {
			return false, fmt.Errorf("handshake: %w", err)
		}
		if err := stream.SendMsg(hello); err != nil {
			return false, streamErr(stream, err)
		}
		d.countSent(len(hello))
	}

	out := make(chan []byte, sendBuffer)
	d.mu.Lock()
	d.out = out
	d.metrics.ProbeFailures = 0
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.out = nil
		d.mu.Unlock()
	}()
	d.state.Publish(link.StateUp, "connected")

	recvDone := make(chan error, 1)
	var gotFrame bool
	go func() {
		for {
			var b []byte
			if err := stream.RecvMsg(&b); err != nil {
				recvDone <- err
				return
			}
			gotFrame = true
			d.countReceived(len(b))
			select {
			case d.rx <- link.RxFrame{Payload: b, ReceivedAt: time.Now()}:
			case <-sctx.Done():
				recvDone <- sctx.Err()
				return
			}
		}
	}()
	wait := func(err error) (bool, error) {
		cancel()
		rerr := <-recvDone
		if err == nil {
			err = rerr
		}
		return gotFrame, err
	}
	for {
		select {
		case <-ctx.Done():
			return wait(ctx.Err())
		case err := <-recvDone:
			return gotFrame, err
		case f := <-out:
			if err := stream.SendMsg(f); err != nil {
				return wait(nil)
			}
			d.countSent(len(f))
		}
	}
}

func streamErr(stream grpc.ClientStream, sendErr error) error {
	var b []byte
	if err := stream.RecvMsg(&b); err != nil {
		return err
	}
	return sendErr
}

func (d *Driver) countSent(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metrics.BytesSent += uint64(n)
	d.metrics.FramesSent++
}

func (d *Driver) countReceived(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metrics.BytesReceived += uint64(n)
	d.metrics.FramesReceived++
	d.metrics.LastRx = time.Now()
}

type rawCodec struct{}

func (rawCodec) Name() string { return "proto" }

func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("ipsession: cannot send %T", v)
	}
	return mem.BufferSlice{mem.SliceBuffer(b)}, nil
}

func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	p, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("ipsession: cannot receive into %T", v)
	}
	*p = data.Materialize()
	return nil
}
