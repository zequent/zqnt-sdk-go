// Command zqnt-edge-node runs an Edge Node: Device Adapters attach on its local socket, and it
// carries their telemetry, alerts and command events to the platform over its link, storing them
// while the link is down, and runs the commands the platform delivers on the adapters.
//
//	zqnt-edge-node --config /etc/zqnt-edge-node/node.yaml
//	zqnt-edge-node --config node.yaml --check-config
//	zqnt-edge-node --version
//	zqnt-edge-node enroll --config node.yaml --claim-code ZQ-ABCDE-FGHJK [--platform host:port]
//	zqnt-edge-node enroll --config node.yaml --provisioning provisioning.json
//	zqnt-edge-node identity --config node.yaml
//	zqnt-edge-node rotate-key --config node.yaml
//	zqnt-edge-node renew-credential --config node.yaml
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
	if len(args) > 0 {
		if cmd, ok := commands[args[0]]; ok {
			return cmd(args[1:], stdout, stderr)
		}
	}
	fl := flag.NewFlagSet("zqnt-edge-node", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { usage(fl) }
	configPath := fl.String("config", defaultConfig, "node configuration file")
	showVersion := fl.Bool("version", false, "print the version and exit")
	checkOnly := fl.Bool("check-config", false, "validate the configuration and the key store and exit")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", fl.Arg(0))
		usage(fl)
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
		return checkConfig(*configPath, cfg, stdout, stderr)
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

func usage(fl *flag.FlagSet) {
	_, _ = fmt.Fprintf(fl.Output(), `Usage:
  zqnt-edge-node [--config FILE]                 run the node
  zqnt-edge-node --config FILE --check-config    validate the configuration and exit
  zqnt-edge-node enroll --config FILE --claim-code CODE [--platform HOST:PORT]
  zqnt-edge-node enroll --config FILE --provisioning FILE | --provisioning-qr PAYLOAD
  zqnt-edge-node identity --config FILE          show the node id, key and anchor fingerprints
  zqnt-edge-node rotate-key --config FILE        replace the identity key
  zqnt-edge-node renew-credential --config FILE  renew the node credential now

Flags:
`)
	fl.PrintDefaults()
}

func checkConfig(path string, cfg node.Config, stdout, stderr io.Writer) int {
	if err := cfg.Identity.Check(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	status, err := enrollmentStatus(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: configuration is valid; %s\n", path, status)
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
