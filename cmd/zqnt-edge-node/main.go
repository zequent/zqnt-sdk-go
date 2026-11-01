// Command zqnt-edge-node runs an Edge Node: Device Adapters attach on its local socket, and it
// carries their telemetry, alerts and command events to the platform over its link, storing them
// while the link is down, and runs the commands the platform delivers on the adapters.
//
//	zqnt-edge-node --config /etc/zqnt-edge-node/node.yaml
//	zqnt-edge-node --config node.yaml --check-config
//	zqnt-edge-node --version
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	_ "github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link/ipsession"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/node"
)

const defaultConfig = "/etc/zqnt-edge-node/node.yaml"

// version is set at build time: -ldflags "-X main.version=v3.1.0".
var version string

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("zqnt-edge-node", flag.ContinueOnError)
	fl.SetOutput(stderr)
	configPath := fl.String("config", defaultConfig, "node configuration file")
	showVersion := fl.Bool("version", false, "print the version and exit")
	checkOnly := fl.Bool("check-config", false, "validate the configuration and exit")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "zqnt-edge-node", buildVersion())
		return 0
	}
	cfg, err := node.LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if *checkOnly {
		_, _ = fmt.Fprintf(stdout, "%s: configuration is valid\n", *configPath)
		return 0
	}

	log := newLogger(cfg.Log, stderr)
	n, err := node.New(node.Options{Config: cfg, Version: buildVersion(), Logger: log})
	if err != nil {
		log.Error("edge node did not start", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := n.Run(ctx); err != nil {
		log.Error("edge node failed", "error", err)
		return 1
	}
	return 0
}

func newLogger(cfg node.LogConfig, w io.Writer) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(strings.ToUpper(cfg.Level)))
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
