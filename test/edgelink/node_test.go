//go:build integration

package edgelink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const assetSN = "SIM-TOX-1"

// buildNode compiles zqnt-edge-node from this checkout.
func buildNode(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "zqnt-edge-node")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/zqnt-edge-node")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build zqnt-edge-node: %v\n%s", err, out)
	}
	return bin
}

// nodeFiles is the node's working directory: configuration, identity, store and socket. It
// lives under /tmp directly, since the socket path has to stay short.
type nodeFiles struct {
	dir, config, socket, log string
}

func writeNodeConfig(t *testing.T, s *stack) nodeFiles {
	t.Helper()
	dir, err := os.MkdirTemp("", "zqnt-node-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	logDir := os.Getenv("ZQNT_TEST_LOG_DIR")
	if logDir == "" {
		logDir = dir
	} else if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := nodeFiles{
		dir:    dir,
		config: filepath.Join(dir, "node.yaml"),
		socket: filepath.Join(dir, "node.sock"),
		log:    filepath.Join(logDir, "node.log"),
	}
	config := fmt.Sprintf(`identity:
  key_store: software-dev
  dev: true
  dir: %s
assets: [%s]
store:
  path: %s
local:
  socket: %s
report_interval: 10s
log:
  level: info
links:
  - id: lte
    driver: ip-session
    endpoint: 127.0.0.1:%d
    ca_file: %s
    server_name: edge-link
    min_backoff: 500ms
    max_backoff: 2s
    keepalive: 120s
`, filepath.Join(dir, "identity"), assetSN, filepath.Join(dir, "node.db"), f.socket, s.ports.proxy, s.certFile())
	if err := os.WriteFile(f.config, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func enrollNode(t *testing.T, bin string, f nodeFiles, claimCode string) {
	t.Helper()
	cmd := exec.Command(bin, "enroll", "--config", f.config, "--claim-code", claimCode, "--display-name", "toxiproxy test node")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("enroll: %v\n%s", err, out)
	}
	t.Logf("enroll:\n%s", strings.TrimSpace(string(out)))
}

// nodeProcess is the running zqnt-edge-node, its log going to a file and the test log.
type nodeProcess struct {
	t    *testing.T
	cmd  *exec.Cmd
	done chan error
	log  *os.File
}

func startNode(t *testing.T, bin string, f nodeFiles) *nodeProcess {
	t.Helper()
	logFile, err := os.Create(f.log)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config", f.config)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start node: %v", err)
	}
	n := &nodeProcess{t: t, cmd: cmd, done: make(chan error, 1), log: logFile}
	go n.copyLog(stderr)
	go func() { n.done <- cmd.Wait() }()
	t.Cleanup(n.stop)
	waitFor(t, "node socket", 30*time.Second, func() bool {
		_, err := os.Stat(f.socket)
		return err == nil
	})
	return n
}

func (n *nodeProcess) copyLog(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(n.log, line)
		if strings.Contains(line, "level=WARN") || strings.Contains(line, "level=ERROR") {
			n.t.Logf("node: %s", line)
		}
	}
}

func (n *nodeProcess) stop() {
	select {
	case <-n.done:
		return
	default:
	}
	_ = n.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-n.done:
	case <-time.After(15 * time.Second):
		_ = n.cmd.Process.Kill()
		<-n.done
	}
	_ = n.log.Close()
}

func (n *nodeProcess) exited() bool {
	select {
	case <-n.done:
		return true
	default:
		return false
	}
}

// producer streams numbered telemetry samples into the node's local socket, as an adapter does.
type producer struct {
	t        *testing.T
	socket   string
	every    time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	produced int64
}

func startProducer(t *testing.T, socket string, every time.Duration) *producer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &producer{t: t, socket: socket, every: every, cancel: cancel, done: make(chan struct{})}
	go p.run(ctx)
	t.Cleanup(p.stop)
	return p
}

func (p *producer) stop() {
	p.cancel()
	<-p.done
}

// count is how many samples went into the node so far; every sample carries its number in
// details.seq, from 1 up.
func (p *producer) count() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.produced
}

func (p *producer) run(ctx context.Context) {
	defer close(p.done)
	conn, err := grpc.NewClient("unix://"+p.socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		p.t.Errorf("producer: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()
	client := telemetryv3.NewTelemetryIngestServiceClient(conn)
	ticker := time.NewTicker(p.every)
	defer ticker.Stop()
	var stream grpc.ClientStreamingClient[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse]
	for {
		select {
		case <-ctx.Done():
			if stream != nil {
				_, _ = stream.CloseAndRecv()
			}
			return
		case <-ticker.C:
		}
		if stream == nil {
			if stream, err = client.PublishTelemetry(ctx); err != nil {
				continue
			}
		}
		p.mu.Lock()
		seq := p.produced + 1
		p.mu.Unlock()
		if err := stream.Send(sample(seq)); err != nil {
			_, recvErr := stream.CloseAndRecv()
			if !errors.Is(err, io.EOF) || recvErr != nil {
				p.t.Logf("producer: sample %d not taken: %v (%v)", seq, err, recvErr)
			}
			stream = nil
			continue
		}
		p.mu.Lock()
		p.produced = seq
		p.mu.Unlock()
	}
}

func sample(seq int64) *telemetryv3.PublishTelemetryRequest {
	battery := 80.0
	return &telemetryv3.PublishTelemetryRequest{Sample: &telemetryv3.TelemetrySample{
		Asset:          &commonv3.AssetRef{Sn: assetSN},
		ObservedAt:     timestamppb.Now(),
		BatteryPercent: &battery,
		Details: &structpb.Struct{Fields: map[string]*structpb.Value{
			"seq": structpb.NewNumberValue(float64(seq)),
		}},
	}}
}

// liveData reads the counts of the live-data stand-in (livedata/main.go) in the stack.
type liveData struct {
	t    *testing.T
	base string
}

type liveDataStats struct {
	Total    int64   `json:"total"`
	Missing  []int64 `json:"missing"`
	Repeated []int64 `json:"repeated"`
}

func (s *stack) liveData() *liveData {
	l := &liveData{t: s.t, base: fmt.Sprintf("http://127.0.0.1:%d/stats", s.ports.liveData)}
	waitFor(s.t, "live-data stand-in", time.Minute, func() bool { return httpOK(l.base) })
	return l
}

func (l *liveData) stats(through int64) liveDataStats {
	l.t.Helper()
	body, err := httpGet(fmt.Sprintf("%s?through=%d", l.base, through))
	if err != nil {
		l.t.Fatalf("live-data stand-in: %v", err)
	}
	var s liveDataStats
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		l.t.Fatalf("live-data stand-in: %v", err)
	}
	return s
}

// delivered is how many samples arrived in all, replays included.
func (l *liveData) delivered() int64 { return l.stats(0).Total }

// missing lists the sample numbers up to through that never arrived.
func (l *liveData) missing(through int64) []int64 { return l.stats(through).Missing }

// repeated lists the sample numbers that arrived more than once.
func (l *liveData) repeated() []int64 { return l.stats(0).Repeated }

// envelopeOutcomes reads edge-link's edge_link_envelopes counter per outcome from its metrics.
func envelopeOutcomes(t *testing.T, port int) map[string]float64 {
	t.Helper()
	resp, err := httpGet(fmt.Sprintf("http://127.0.0.1:%d/q/metrics", port))
	if err != nil {
		t.Fatalf("edge-link metrics: %v", err)
	}
	outcomes := map[string]float64{}
	for _, line := range strings.Split(resp, "\n") {
		if !strings.HasPrefix(line, "edge_link_envelopes_total{") {
			continue
		}
		_, rest, _ := strings.Cut(line, `outcome="`)
		outcome, rest, _ := strings.Cut(rest, `"`)
		_, value, _ := strings.Cut(rest, "} ")
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil {
			outcomes[outcome] = v
		}
	}
	return outcomes
}
