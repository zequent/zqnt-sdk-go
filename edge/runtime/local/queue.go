package local

import (
	"context"
	"fmt"
	"math"
	"time"

	edgelinkv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/edgelink/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/store"
)

// LaneStats is what one lane of the node's queue holds, and how hard adapters should push into it.
type LaneStats struct {
	Lane     edgelinkv3.Lane
	Items    int
	Bytes    int64
	MaxItems int
	MaxBytes int64
	// Fill is Items or Bytes against the quota, whichever is fuller, 0..1.
	Fill    float64
	Dropped uint64
	Oldest  time.Time
	// Throttle is the share of its normal rate an adapter should send into this lane, 0.1..1.
	Throttle float64
}

// QueueStats is the node's queue as adapters see it.
type QueueStats struct {
	// Connected reports whether the node has a session to the platform now.
	Connected bool
	Lanes     []LaneStats
}

// Lane returns the stats of one lane.
func (q QueueStats) Lane(l edgelinkv3.Lane) (LaneStats, bool) {
	for _, s := range q.Lanes {
		if s.Lane == l {
			return s, true
		}
	}
	return LaneStats{}, false
}

// Throttle is the share of its normal rate an adapter should send at for a lane that is fill full:
// 1 up to half full, then down linearly to 0.1 when full, in steps of 0.1. A full lane still
// accepts items; it thins, replaces or drops by its policy.
func Throttle(fill float64) float64 {
	const free, floor = 0.5, 0.1
	if fill <= free {
		return 1
	}
	return max(floor, math.Round(10*(1-(1-floor)*(min(fill, 1)-free)/(1-free)))/10)
}

func queueStats(st *store.Store, connected bool) (QueueStats, error) {
	backlog, err := st.Backlog()
	if err != nil {
		return QueueStats{}, err
	}
	q := QueueStats{Connected: connected}
	for _, b := range backlog {
		fill := b.Fill()
		q.Lanes = append(q.Lanes, LaneStats{
			Lane: b.Lane, Items: b.Items, Bytes: b.Bytes, MaxItems: b.MaxItems, MaxBytes: b.MaxBytes,
			Fill: fill, Dropped: b.Dropped, Oldest: b.Oldest, Throttle: Throttle(fill),
		})
	}
	return q, nil
}

// Struct is the wire form of the stats on the local queue service:
//
//	{"connected": bool, "lanes": [{"lane": "LANE_TELEMETRY", "items": n, "bytes": n, "max_items": n,
//	  "max_bytes": n, "fill": 0..1, "dropped": n, "oldest": RFC 3339 or "", "throttle": 0.1..1}]}
func (q QueueStats) Struct() *structpb.Struct {
	num := structpb.NewNumberValue
	lanes := make([]*structpb.Value, 0, len(q.Lanes))
	for _, l := range q.Lanes {
		oldest := ""
		if !l.Oldest.IsZero() {
			oldest = l.Oldest.UTC().Format(time.RFC3339Nano)
		}
		lanes = append(lanes, structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{
			"lane":      structpb.NewStringValue(l.Lane.String()),
			"items":     num(float64(l.Items)),
			"bytes":     num(float64(l.Bytes)),
			"max_items": num(float64(l.MaxItems)),
			"max_bytes": num(float64(l.MaxBytes)),
			"fill":      num(l.Fill),
			"dropped":   num(float64(l.Dropped)),
			"oldest":    structpb.NewStringValue(oldest),
			"throttle":  num(l.Throttle),
		}}))
	}
	return &structpb.Struct{Fields: map[string]*structpb.Value{
		"connected": structpb.NewBoolValue(q.Connected),
		"lanes":     structpb.NewListValue(&structpb.ListValue{Values: lanes}),
	}}
}

// QueueStatsFromStruct reads the wire form back.
func QueueStatsFromStruct(s *structpb.Struct) (QueueStats, error) {
	q := QueueStats{Connected: s.GetFields()["connected"].GetBoolValue()}
	for _, v := range s.GetFields()["lanes"].GetListValue().GetValues() {
		f := v.GetStructValue().GetFields()
		lane, ok := edgelinkv3.Lane_value[f["lane"].GetStringValue()]
		if !ok {
			return QueueStats{}, fmt.Errorf("local: unknown lane %q", f["lane"].GetStringValue())
		}
		l := LaneStats{
			Lane: edgelinkv3.Lane(lane), Items: int(f["items"].GetNumberValue()), Bytes: int64(f["bytes"].GetNumberValue()),
			MaxItems: int(f["max_items"].GetNumberValue()), MaxBytes: int64(f["max_bytes"].GetNumberValue()),
			Fill: f["fill"].GetNumberValue(), Dropped: uint64(f["dropped"].GetNumberValue()), Throttle: f["throttle"].GetNumberValue(),
		}
		if o := f["oldest"].GetStringValue(); o != "" {
			t, err := time.Parse(time.RFC3339Nano, o)
			if err != nil {
				return QueueStats{}, fmt.Errorf("local: oldest: %w", err)
			}
			l.Oldest = t
		}
		q.Lanes = append(q.Lanes, l)
	}
	return q, nil
}

// QueueServiceName is the local gRPC service adapters read the node's queue from. It exists on the
// node socket only and carries google.protobuf messages, so any language can call it without new
// generated code:
//
//	rpc GetQueueStats(google.protobuf.Empty) returns (google.protobuf.Struct)
//	rpc WatchQueueStats(google.protobuf.Empty) returns (stream google.protobuf.Struct)
//
// WatchQueueStats sends the stats at once, then whenever a lane's throttle or the connection
// changes, and at least every WatchHeartbeat.
const QueueServiceName = "zqnt.edgenode.local.v1.NodeQueueService"

// WatchHeartbeat is the longest WatchQueueStats stays silent.
const WatchHeartbeat = 10 * time.Second

type queueServer interface {
	getQueueStats(context.Context) (*structpb.Struct, error)
	watchQueueStats(grpc.ServerStream) error
}

var queueServiceDesc = grpc.ServiceDesc{
	ServiceName: QueueServiceName,
	HandlerType: (*queueServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "GetQueueStats",
		Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := new(emptypb.Empty)
			if err := dec(in); err != nil {
				return nil, err
			}
			call := func(ctx context.Context, _ any) (any, error) { return srv.(queueServer).getQueueStats(ctx) }
			if interceptor == nil {
				return call(ctx, in)
			}
			return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + QueueServiceName + "/GetQueueStats"}, call)
		},
	}},
	Streams: []grpc.StreamDesc{{
		StreamName:    "WatchQueueStats",
		ServerStreams: true,
		Handler: func(srv any, stream grpc.ServerStream) error {
			if err := stream.RecvMsg(new(emptypb.Empty)); err != nil {
				return err
			}
			return srv.(queueServer).watchQueueStats(stream)
		},
	}},
}

// QueueClient reads the node's queue over the node socket.
type QueueClient struct{ cc grpc.ClientConnInterface }

// NewQueueClient uses a connection to the node socket.
func NewQueueClient(cc grpc.ClientConnInterface) *QueueClient { return &QueueClient{cc: cc} }

// Get returns the stats now.
func (c *QueueClient) Get(ctx context.Context) (QueueStats, error) {
	out := new(structpb.Struct)
	if err := c.cc.Invoke(ctx, "/"+QueueServiceName+"/GetQueueStats", &emptypb.Empty{}, out); err != nil {
		return QueueStats{}, err
	}
	return QueueStatsFromStruct(out)
}

// Watch calls fn with the stats as they change until ctx ends, fn returns an error or the stream
// fails.
func (c *QueueClient) Watch(ctx context.Context, fn func(QueueStats) error) error {
	stream, err := c.cc.NewStream(ctx, &queueServiceDesc.Streams[0], "/"+QueueServiceName+"/WatchQueueStats")
	if err != nil {
		return err
	}
	if err := stream.SendMsg(&emptypb.Empty{}); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	for {
		msg := new(structpb.Struct)
		if err := stream.RecvMsg(msg); err != nil {
			return err
		}
		q, err := QueueStatsFromStruct(msg)
		if err != nil {
			return err
		}
		if err := fn(q); err != nil {
			return err
		}
	}
}
