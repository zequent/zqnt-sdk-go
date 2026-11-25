// Command livedata stands in for live-data's telemetry ingest in the edge-link outage test: it
// takes what edge-link hands off and counts how often each numbered sample (details.seq)
// arrived. The test reads the counts over HTTP:
//
//	GET /stats?through=N  ->  {"total": deliveries, "missing": [seq...], "repeated": [seq...]}
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"

	telemetryv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/telemetry/v3"
	"google.golang.org/grpc"
)

func main() {
	grpcAddr := flag.String("grpc", ":8003", "where edge-link hands samples off")
	httpAddr := flag.String("http", ":8080", "where the counts are read")
	flag.Parse()

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatal(err)
	}
	c := &counter{seen: map[int64]int{}}
	server := grpc.NewServer()
	telemetryv3.RegisterTelemetryIngestServiceServer(server, c)
	go func() { log.Fatal(server.Serve(lis)) }()

	http.HandleFunc("/stats", c.stats)
	log.Printf("live-data stand-in: ingest on %s, stats on %s", *grpcAddr, *httpAddr)
	log.Fatal(http.ListenAndServe(*httpAddr, nil))
}

type counter struct {
	telemetryv3.UnimplementedTelemetryIngestServiceServer
	mu    sync.Mutex
	seen  map[int64]int
	total int64
}

func (c *counter) PublishTelemetry(stream grpc.ClientStreamingServer[telemetryv3.PublishTelemetryRequest, telemetryv3.PublishTelemetryResponse]) error {
	var resp telemetryv3.PublishTelemetryResponse
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&resp)
		}
		if err != nil {
			return err
		}
		seq := int64(req.GetSample().GetDetails().GetFields()["seq"].GetNumberValue())
		c.mu.Lock()
		c.seen[seq]++
		c.total++
		c.mu.Unlock()
		resp.Accepted++
	}
}

// Stats is what the test asserts on: every sample up to through arrived, none more than once.
type Stats struct {
	Total    int64   `json:"total"`
	Missing  []int64 `json:"missing"`
	Repeated []int64 `json:"repeated"`
}

func (c *counter) stats(w http.ResponseWriter, r *http.Request) {
	through, _ := strconv.ParseInt(r.URL.Query().Get("through"), 10, 64)
	c.mu.Lock()
	s := Stats{Total: c.total, Missing: []int64{}, Repeated: []int64{}}
	for seq := int64(1); seq <= through; seq++ {
		if c.seen[seq] == 0 {
			s.Missing = append(s.Missing, seq)
		}
	}
	for seq, n := range c.seen {
		if n > 1 {
			s.Repeated = append(s.Repeated, seq)
		}
	}
	c.mu.Unlock()
	sort.Slice(s.Repeated, func(i, j int) bool { return s.Repeated[i] < s.Repeated[j] })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}
