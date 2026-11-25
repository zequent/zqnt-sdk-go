package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/identity"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/link"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/node"
)

type command func(args []string, stdout, stderr io.Writer) int

var commands map[string]command

func init() {
	commands = map[string]command{
		"enroll":           enroll,
		"identity":         showIdentity,
		"rotate-key":       rotateKey,
		"renew-credential": renewCredential,
	}
}

func commandFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fl := flag.NewFlagSet("zqnt-edge-node "+name, flag.ContinueOnError)
	fl.SetOutput(stderr)
	return fl, fl.String("config", defaultConfig, "node configuration file")
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// fingerprint writes a key id as the console shows it: hex in groups of four.
func fingerprint(id []byte) string {
	h := hex.EncodeToString(id)
	var groups []string
	for len(h) > 4 {
		groups, h = append(groups, h[:4]), h[4:]
	}
	return strings.Join(append(groups, h), " ")
}

func enroll(args []string, stdout, stderr io.Writer) int {
	fl, configPath := commandFlags("enroll", stderr)
	claimCode := fl.String("claim-code", "", "enrollment claim code from the console (ZQ-XXXXX-XXXXX)")
	platform := fl.String("platform", "", "edge-link address host:port (default: the ip-session link's endpoint)")
	caFile := fl.String("ca-file", "", "PEM roots for the platform certificate (default: the link's ca_file)")
	serverName := fl.String("server-name", "", "TLS server name of the platform (default: the link's server_name)")
	insecure := fl.Bool("insecure", false, "enroll without TLS (local stacks only; the platform refuses it otherwise)")
	provisioningFile := fl.String("provisioning", "", "provisioning file that pins the installation and its anchors")
	provisioningQR := fl.String("provisioning-qr", "", "provisioning QR payload ("+identity.ProvisioningQRPrefix+"...)")
	displayName := fl.String("display-name", "", "name the console shows for the node")
	reenroll := fl.Bool("reenroll", false, "replace this node's enrollment (with a re-enrollment claim code)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	cfg, err := node.LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var prov *identity.Provisioning
	switch {
	case *provisioningFile != "" && *provisioningQR != "":
		_, _ = fmt.Fprintln(stderr, "enroll: --provisioning and --provisioning-qr: give one")
		return 2
	case *provisioningFile != "":
		prov, err = identity.ReadProvisioning(*provisioningFile)
	case *provisioningQR != "":
		prov, err = identity.ParseProvisioningQR(*provisioningQR)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	target, err := linkPlatform(cfg.Config)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	code := *claimCode
	if prov != nil {
		if code == "" {
			code = prov.ClaimCode
		}
		if prov.Platform.Endpoint != "" {
			target = identity.Platform{Endpoint: prov.Platform.Endpoint, ServerName: prov.Platform.ServerName}
		}
		if prov.Platform.CAPEM != "" {
			target.CAFile = filepath.Join(cfg.Identity.Dir, identity.CAFile)
			err := os.MkdirAll(cfg.Identity.Dir, 0o700)
			if err == nil {
				err = os.WriteFile(target.CAFile, []byte(prov.Platform.CAPEM), 0o600)
			}
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "enroll: ca_pem:", err)
				return 1
			}
		}
	}
	for _, f := range []struct{ value, field *string }{
		{platform, &target.Endpoint}, {caFile, &target.CAFile}, {serverName, &target.ServerName},
	} {
		if *f.value != "" {
			*f.field = *f.value
		}
	}
	if *insecure {
		target.Insecure = true
	}
	if code == "" {
		_, _ = fmt.Fprintln(stderr, "enroll: --claim-code is required (or a provisioning file that carries one)")
		return 2
	}
	if target.Endpoint == "" {
		_, _ = fmt.Fprintln(stderr, "enroll: --platform is required (or an ip-session link with an endpoint)")
		return 2
	}
	suite, _ := cfg.Identity.SuiteID()
	class, _ := cfg.Identity.Class()
	keys, err := cfg.Identity.OpenKeyStore()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = keys.Close() }()
	assets := make([]*edgelinkv3.EnrollingAsset, 0, len(cfg.Assets))
	for _, sn := range cfg.Assets {
		assets = append(assets, &edgelinkv3.EnrollingAsset{Sn: sn})
	}
	ctx, stop := signalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	state, err := identity.Enroll(ctx, identity.EnrollOptions{
		Dir:            identity.Dir(cfg.Identity.Dir),
		Store:          keys,
		ClaimCode:      code,
		Platform:       target,
		Assets:         assets,
		DisplayName:    *displayName,
		RuntimeVersion: buildVersion(),
		Suite:          suite,
		NodeClass:      class,
		Provisioning:   prov,
		Reenroll:       *reenroll,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "enrolled in %s\n", nonEmpty(state.OrganizationName, state.OrganizationID))
	printIdentity(stdout, state)
	if prov == nil {
		_, _ = fmt.Fprintln(stdout, "compare the anchor fingerprint with the one the console shows before you rely on this node")
	}
	return 0
}

// linkPlatform takes the enrollment endpoint from the first ip-session link.
func linkPlatform(cfg link.Config) (identity.Platform, error) {
	for _, l := range cfg.Links {
		if l.Driver != "ip-session" {
			continue
		}
		var p identity.Platform
		var errs []error
		var err error
		p.Endpoint, err = l.String("endpoint", "")
		errs = append(errs, err)
		p.CAFile, err = l.String("ca_file", "")
		errs = append(errs, err)
		p.ServerName, err = l.String("server_name", "")
		errs = append(errs, err)
		p.Insecure, err = l.Bool("insecure", false)
		errs = append(errs, err)
		return p, errors.Join(errs...)
	}
	return identity.Platform{}, nil
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func printIdentity(w io.Writer, s *identity.State) {
	_, _ = fmt.Fprintf(w, "node id:             %s\n", s.NodeID)
	_, _ = fmt.Fprintf(w, "organization id:     %s\n", s.OrganizationID)
	_, _ = fmt.Fprintf(w, "installation id:     %s\n", s.InstallationID)
	_, _ = fmt.Fprintf(w, "node class:          %s\n", s.NodeClass)
	_, _ = fmt.Fprintf(w, "key protection:      %s\n", s.KeyProtection)
	_, _ = fmt.Fprintf(w, "identity key:        %s\n", fingerprint(s.IdentityKeyID))
	for _, a := range s.Anchors {
		_, _ = fmt.Fprintf(w, "platform anchor:     %s\n", fingerprint(a.KeyID))
	}
}

func showIdentity(args []string, stdout, stderr io.Writer) int {
	fl, configPath := commandFlags("identity", stderr)
	if err := fl.Parse(args); err != nil {
		return 2
	}
	cfg, err := node.LoadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	dir := identity.Dir(cfg.Identity.Dir)
	state, err := dir.LoadState()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	printIdentity(stdout, state)
	if _, meta, err := dir.Credential(); err == nil {
		_, _ = fmt.Fprintf(stdout, "credential expires:  %s (renewal from %s)\n",
			meta.ExpiresAt.UTC().Format(time.RFC3339), meta.RenewAt().UTC().Format(time.RFC3339))
	}
	return 0
}

func enrolledNode(configPath string, stderr io.Writer) (identity.Node, func(), bool) {
	cfg, err := node.LoadConfig(configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return identity.Node{}, nil, false
	}
	keys, err := cfg.Identity.OpenKeyStore()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return identity.Node{}, nil, false
	}
	return identity.Node{Dir: identity.Dir(cfg.Identity.Dir), Store: keys}, func() { _ = keys.Close() }, true
}

func rotateKey(args []string, stdout, stderr io.Writer) int {
	fl, configPath := commandFlags("rotate-key", stderr)
	if err := fl.Parse(args); err != nil {
		return 2
	}
	n, done, ok := enrolledNode(*configPath, stderr)
	if !ok {
		return 1
	}
	defer done()
	ctx, stop := signalContext()
	defer stop()
	state, err := n.RotateKey(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "identity key rotated: %s\n", fingerprint(state.IdentityKeyID))
	return 0
}

func renewCredential(args []string, stdout, stderr io.Writer) int {
	fl, configPath := commandFlags("renew-credential", stderr)
	if err := fl.Parse(args); err != nil {
		return 2
	}
	n, done, ok := enrolledNode(*configPath, stderr)
	if !ok {
		return 1
	}
	defer done()
	state, err := n.Dir.LoadState()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	signer, err := identity.NewProvider(n.Dir, n.Store, state)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signalContext()
	defer stop()
	meta, err := n.RenewCredential(ctx, signer)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "credential renewed, expires %s\n", meta.ExpiresAt.UTC().Format(time.RFC3339))
	return 0
}

func enrollmentStatus(cfg node.Config) (string, error) {
	state, err := identity.Dir(cfg.Identity.Dir).LoadState()
	switch {
	case errors.Is(err, identity.ErrNotEnrolled):
		return "not enrolled yet (zqnt-edge-node enroll)", nil
	case err != nil:
		return "", err
	}
	keys, err := cfg.Identity.OpenKeyStore()
	if err != nil {
		return "", err
	}
	defer func() { _ = keys.Close() }()
	p, err := identity.NewProvider(identity.Dir(cfg.Identity.Dir), keys, state)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("enrolled as node %s, identity key %s (%s)", state.NodeID, fingerprint(p.KeyID()), p.Protection()), nil
}
