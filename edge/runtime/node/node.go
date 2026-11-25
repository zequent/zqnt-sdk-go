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
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
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
	// KeyStore holds the identity key; nil opens the configured one.
	KeyStore identity.KeyStore
	// StatusInterval is how often the node checks its session to log connects and losses (1s).
	StatusInterval time.Duration
}

// Node is one Edge Node. New opens its store and socket; Run serves until its context ends.
type Node struct {
	opts     Options
	log      *slog.Logger
	dir      identity.Dir
	keys     identity.KeyStore
	identity *identity.Provider
	store    *store.Store
	driver   link.Driver
	session  *session.Session
	runner   *command.Runner
	server   *local.Server
	lis      net.Listener

	revoked   chan string
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
	n := &Node{opts: opts, dir: identity.Dir(cfg.Identity.Dir), revoked: make(chan string, 1)}
	state, err := n.dir.LoadState()
	if err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	if err := checkEnrollment(cfg.Identity, state); err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	n.log = opts.Logger.With("node_id", state.NodeID)
	n.keys = opts.KeyStore
	if n.keys == nil {
		if n.keys, err = cfg.Identity.OpenKeyStore(); err != nil {
			return nil, fmt.Errorf("node: %w", err)
		}
	}
	if n.identity, err = identity.NewProvider(n.dir, n.keys, state); err != nil {
		return nil, errors.Join(fmt.Errorf("node: %w", err), n.keys.Close())
	}
	trust, err := n.dir.Trust(state)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("node: %w", err), n.keys.Close())
	}

	st, err := store.Open(cfg.Store.Path, store.Options{
		Lanes:         cfg.Lanes(),
		FlushInterval: time.Duration(cfg.Store.FlushInterval),
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("node: %w", err), n.keys.Close())
	}
	n.store = st

	drivers, err := link.Build(withCredential(cfg.Config, n.dir))
	if err != nil {
		return nil, n.abort(fmt.Errorf("node: %w", err))
	}
	n.driver = drivers[0]

	sess, err := session.New(session.Config{
		NodeID:         state.NodeID,
		Assets:         n.Assets,
		RuntimeVersion: opts.Version,
		LinkKind:       cfg.Links[0].Driver,
		Store:          st,
		Driver:         n.driver,
		Identity:       n.identity,
		Trust:          trust,
		Handler:        n.handleDownlink,
		ReportInterval: time.Duration(cfg.ReportInterval),
		OnHandshakeError: func(err error) {
			n.log.Warn("handshake with the platform failed", "error", err)
		},
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

// checkEnrollment refuses an enrollment that does not fit the configuration.
func checkEnrollment(cfg identity.Config, state *identity.State) error {
	var errs []error
	if suite, err := cfg.SuiteID(); err == nil && suite != state.Suite {
		errs = append(errs, fmt.Errorf("enrolled with suite %#04x, configured %s: enroll again to change the suite", state.Suite, cfg.Suite))
	}
	if !strings.EqualFold(cfg.NodeClass, state.NodeClass) {
		errs = append(errs, fmt.Errorf("enrolled as a %s node, configured %s", state.NodeClass, cfg.NodeClass))
	}
	return errors.Join(errs...)
}

// withCredential points IP session links without a token_file at the enrolled credential.
func withCredential(cfg link.Config, dir identity.Dir) link.Config {
	links := make([]link.LinkConfig, len(cfg.Links))
	for i, l := range cfg.Links {
		links[i] = l
		if l.Driver != "ip-session" {
			continue
		}
		if _, set := l.Options["token_file"]; set {
			continue
		}
		opts := make(map[string]any, len(l.Options)+1)
		for k, v := range l.Options {
			opts[k] = v
		}
		opts["token_file"] = dir.CredentialPath()
		links[i].Options = opts
	}
	cfg.Links = links
	return cfg
}

func (n *Node) handleDownlink(ctx context.Context, f *edgelinkv3.DownlinkFrame) error {
	if r := f.GetRevocation(); r != nil && slices.Contains(r.GetNodeIds(), n.identity.State().NodeID) {
		select {
		case n.revoked <- r.GetReason():
		default:
		}
		return nil
	}
	return n.runner.HandleFrame(ctx, f)
}

func (n *Node) abort(err error) error {
	return errors.Join(err, n.store.Close(), n.keys.Close())
}

// NodeID is the id the platform assigned at enrollment.
func (n *Node) NodeID() string { return n.identity.State().NodeID }

// ServedAssets are the assets the platform serves for this node in the current session.
func (n *Node) ServedAssets() []string { return n.session.ServedAssets() }

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
	state := n.identity.State()
	if state.KeyProtection == edgelinkv3.KeyProtection_KEY_PROTECTION_SOFTWARE_DEV.String() {
		n.log.Warn("development node: the identity key is a plain file (key_store software-dev)")
	}

	sessCtx, stopSession := context.WithCancel(context.WithoutCancel(ctx))
	defer stopSession()
	sessDone := make(chan error, 1)
	go func() { sessDone <- n.session.Run(sessCtx) }()
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		identity.Node{Dir: n.dir, Store: n.keys}.RunRenewal(sessCtx, n.identity, n.log)
	}()
	serveDone := make(chan error, 1)
	go func() { serveDone <- n.server.Serve(n.lis) }()

	n.log.Info("edge node started",
		"version", n.opts.Version, "socket", n.SocketPath(), "store", cfg.Store.Path,
		"link", n.driver.ID(), "driver", cfg.Links[0].Driver, "assets", cfg.Assets,
		"organization", state.OrganizationName, "identity_key_id", fmt.Sprintf("%x", []byte(state.IdentityKeyID)),
		"key_protection", n.identity.Protection(), "node_class", state.NodeClass)

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
		case reason := <-n.revoked:
			runErr = fmt.Errorf("node: revoked by the platform: %s", reason)
			n.log.Error("this node was revoked: its identity and credential are erased; enroll again with a re-enrollment claim", "reason", reason)
			stopSession()
			runErr = errors.Join(runErr, n.erase())
			break loop
		case <-status.C:
			now, id := n.session.Connected(), n.session.SessionID()
			switch {
			case now && (!connected || id != sessionID):
				served := n.session.ServedAssets()
				n.log.Info("connected to the platform", "session_id", id, "link", n.driver.ID(), "assets", n.Assets(), "served", served)
				if unserved := unservedAssets(n.Assets(), served); len(unserved) > 0 {
					n.log.Warn("the platform does not serve these assets for this node: bind them to the node in the console", "assets", unserved)
				}
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
	<-renewDone
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
		err = errors.Join(err, n.store.Close(), n.keys.Close())
	})
	return err
}

// erase removes the identity of a revoked node: credential, enrollment and keys.
func (n *Node) erase() error {
	return errors.Join(n.dir.Erase(), n.keys.Delete(identity.KeyActive), n.keys.Delete(identity.KeyNext))
}

func unservedAssets(announced, served []string) []string {
	var out []string
	for _, sn := range announced {
		if !slices.Contains(served, sn) {
			out = append(out, sn)
		}
	}
	return out
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
