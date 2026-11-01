// Package node wires the edge runtime into one Edge Node: the durable store, a session on the
// configured link, the command Runner and the local socket Device Adapters attach to. The
// zqnt-edge-node binary is a thin main around it; link drivers are registered by importing them.
package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/command"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/session"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// Options configure a Node.
type Options struct {
	Config Config
	// Version is reported in Hello as the runtime version.
	Version string
	Logger  *slog.Logger
	// Signer signs the Hello with the node's identity key. Node identity and enrollment are not
	// built yet (zqnt-api#40): nil runs the node unsigned.
	Signer session.Signer
	// StatusInterval is how often the node checks its session to log connects and losses (1s).
	StatusInterval time.Duration
}

// Node is one Edge Node. New opens its store and socket; Run serves until its context ends.
type Node struct {
	opts    Options
	log     *slog.Logger
	store   *store.Store
	driver  link.Driver
	session *session.Session
	runner  *command.Runner
	server  *local.Server
	lis     net.Listener

	closeOnce sync.Once
}

// New validates the configuration, opens the store, builds the link, session, command Runner and
// local server, and listens on the node socket.
func New(opts Options) (*Node, error) {
	cfg := opts.Config
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.StatusInterval <= 0 {
		opts.StatusInterval = time.Second
	}
	n := &Node{opts: opts, log: opts.Logger.With("node_id", cfg.NodeID)}

	st, err := store.Open(cfg.Store.Path, store.Options{
		Lanes:         cfg.Lanes(),
		FlushInterval: time.Duration(cfg.Store.FlushInterval),
	})
	if err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	n.store = st

	drivers, err := link.Build(cfg.Config)
	if err != nil {
		return nil, n.abort(fmt.Errorf("node: %w", err))
	}
	n.driver = drivers[0]

	sess, err := session.New(session.Config{
		NodeID:         cfg.NodeID,
		Assets:         n.Assets,
		RuntimeVersion: opts.Version,
		LinkKind:       cfg.Links[0].Driver,
		Store:          st,
		Driver:         n.driver,
		Signer:         opts.Signer,
		Handler:        n.handleDownlink,
		ReportInterval: time.Duration(cfg.ReportInterval),
	})
	if err != nil {
		return nil, n.abort(fmt.Errorf("node: %w", err))
	}
	n.session = sess

	srv, err := local.New(local.Config{
		Store:                  st,
		Publisher:              sess,
		Connected:              sess.Connected,
		DefaultAdapterEndpoint: cfg.Local.DefaultAdapterEndpoint,
	})
	if err != nil {
		return nil, n.abort(fmt.Errorf("node: %w", err))
	}
	n.server = srv

	runner, err := command.New(command.Config{Store: st, Publisher: sess, Handler: srv.Handler()})
	if err != nil {
		return nil, n.abort(fmt.Errorf("node: %w", err))
	}
	n.runner = runner

	gid := 0
	if cfg.Local.Group != "" {
		if gid, err = lookupGroup(cfg.Local.Group); err != nil {
			return nil, n.abort(fmt.Errorf("node: local.group: %w", err))
		}
	}
	lis, err := local.Listen(cfg.Local.Socket, local.ListenOptions{Mode: fs.FileMode(cfg.Local.Mode), GID: gid})
	if err != nil {
		return nil, n.abort(fmt.Errorf("node: %w", err))
	}
	n.lis = lis
	return n, nil
}

func (n *Node) handleDownlink(ctx context.Context, f *edgelinkv3.DownlinkFrame) error {
	return n.runner.HandleFrame(ctx, f)
}

func (n *Node) abort(err error) error {
	return errors.Join(err, n.store.Close())
}

// SocketPath is where adapters attach.
func (n *Node) SocketPath() string { return n.opts.Config.Local.Socket }

// Connected reports whether the node has a session to the platform.
func (n *Node) Connected() bool { return n.session.Connected() }

// Assets is what the next Hello announces: the configured assets and every asset an adapter
// registered on the local socket, sorted.
func (n *Node) Assets() []string {
	set := map[string]struct{}{}
	for _, sn := range n.opts.Config.Assets {
		set[sn] = struct{}{}
	}
	for sn := range n.server.Assets() {
		set[sn] = struct{}{}
	}
	return slices.Sorted(maps.Keys(set))
}

// Run reports commands a previous run left unfinished, then serves adapters and runs the session
// until ctx ends or the link fails, and shuts down: it stops accepting adapters, waits up to
// shutdown_timeout for running commands, closes the link and flushes the store. It returns nil
// after a shutdown asked for by ctx.
func (n *Node) Run(ctx context.Context) error {
	cfg := n.opts.Config
	if err := n.runner.Recover(ctx); err != nil {
		return errors.Join(fmt.Errorf("node: recover commands: %w", err), n.shutdown())
	}
	if n.opts.Signer == nil {
		n.log.Warn("node identity is not configured: the Hello goes out unsigned")
	}

	sessCtx, stopSession := context.WithCancel(context.WithoutCancel(ctx))
	defer stopSession()
	sessDone := make(chan error, 1)
	go func() { sessDone <- n.session.Run(sessCtx) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- n.server.Serve(n.lis) }()

	n.log.Info("edge node started",
		"version", n.opts.Version, "socket", n.SocketPath(), "store", cfg.Store.Path,
		"link", n.driver.ID(), "driver", cfg.Links[0].Driver, "assets", cfg.Assets)

	status := time.NewTicker(n.opts.StatusInterval)
	defer status.Stop()
	stats := time.NewTicker(time.Duration(cfg.ReportInterval))
	defer stats.Stop()

	var runErr error
	connected, sessionID := false, ""
loop:
	for {
		select {
		case <-ctx.Done():
			n.log.Info("edge node stopping", "cause", context.Cause(ctx))
			break loop
		case err := <-sessDone:
			sessDone = nil
			runErr = fmt.Errorf("node: session ended: %w", err)
			n.log.Error("session ended", "error", err)
			break loop
		case err := <-serveDone:
			serveDone = nil
			runErr = fmt.Errorf("node: local socket: %w", err)
			n.log.Error("local socket stopped", "error", err)
			break loop
		case <-status.C:
			now, id := n.session.Connected(), n.session.SessionID()
			switch {
			case now && (!connected || id != sessionID):
				n.log.Info("connected to the platform", "session_id", id, "link", n.driver.ID(), "assets", n.Assets())
			case !now && connected:
				n.log.Warn("platform session lost", "session_id", sessionID, "link", n.driver.ID())
			}
			connected, sessionID = now, id
		case <-stats.C:
			n.logStats()
		}
	}

	stopErr := n.server.Stop()
	waited := make(chan struct{})
	go func() { n.runner.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(time.Duration(cfg.ShutdownTimeout)):
		n.log.Warn("commands still running at shutdown; they are reported as interrupted on the next start")
	}
	stopSession()
	if sessDone != nil {
		if err := <-sessDone; err != nil && !errors.Is(err, context.Canceled) {
			n.log.Warn("session closed with an error", "error", err)
		}
	}
	if serveDone != nil {
		<-serveDone
	}
	err := errors.Join(runErr, stopErr, n.shutdown())
	n.log.Info("edge node stopped")
	return err
}

func (n *Node) shutdown() error {
	var err error
	n.closeOnce.Do(func() {
		_ = n.lis.Close()
		_ = n.server.Stop()
		if ferr := n.store.Flush(); ferr != nil {
			err = fmt.Errorf("node: flush store: %w", ferr)
		}
		err = errors.Join(err, n.store.Close())
	})
	return err
}

// Close releases what New opened without running the node.
func (n *Node) Close() error { return n.shutdown() }

func (n *Node) logStats() {
	m := n.driver.Metrics()
	attrs := []any{
		"connected", n.session.Connected(),
		"session_id", n.session.SessionID(),
		"rtt_ms", n.session.RTT().Milliseconds(),
		"assets", len(n.Assets()),
		slog.Group("link", "id", n.driver.ID(),
			"frames_sent", m.FramesSent, "frames_received", m.FramesReceived, "frames_dropped", m.FramesDropped,
			"bytes_sent", m.BytesSent, "bytes_received", m.BytesReceived),
	}
	backlog, err := n.store.Backlog()
	if err != nil {
		n.log.Warn("store backlog unreadable", "error", err)
	}
	for _, b := range backlog {
		name := strings.ToLower(strings.TrimPrefix(b.Lane.String(), "LANE_"))
		attrs = append(attrs, slog.Group("lane_"+name, "items", b.Items, "bytes", b.Bytes, "dropped", b.Dropped))
	}
	attrs = append(attrs, "in_flight", n.store.InFlight())
	n.log.Info("node stats", attrs...)
}
