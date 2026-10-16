package streams_test

import (
	"context"
	"net"
	"testing"
	"time"

	commonv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/common/v3"
	streamv3 "github.com/zequent/zqnt-api/go/v3/gen/zqnt/stream/v3"
	"github.com/zequent/zqnt-sdk-go/v3/client/streams"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeLiveStreams struct {
	streamv3.UnimplementedLiveStreamServiceServer

	request *streamv3.ListLiveStreamsRequest
	refuse  error
	streams []*streamv3.LiveStream
}

func (f *fakeLiveStreams) ListLiveStreams(_ context.Context, req *streamv3.ListLiveStreamsRequest) (*streamv3.ListLiveStreamsResponse, error) {
	f.request = req
	if f.refuse != nil {
		return nil, f.refuse
	}
	return &streamv3.ListLiveStreamsResponse{Streams: f.streams}, nil
}

func dial(t *testing.T, fake *fakeLiveStreams) *streams.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	streamv3.RegisterLiveStreamServiceServer(server, fake)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return streams.New(conn)
}

func timeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestListLiveStreamsReturnsTheStreamsOfTheOrganization(t *testing.T) {
	since := time.Unix(1_700_000_000, 0)
	fake := &fakeLiveStreams{streams: []*streamv3.LiveStream{{
		Asset:   &commonv3.AssetRef{Sn: "DRONE-1"},
		Path:    "live/DRONE-1/drone",
		Since:   timestamppb.New(since),
		Viewers: 2,
	}}}

	live, err := dial(t, fake).ListLiveStreams(timeout(t))
	if err != nil {
		t.Fatalf("ListLiveStreams: %v", err)
	}

	if len(live) != 1 {
		t.Fatalf("streams = %d, want 1", len(live))
	}
	got := live[0]
	if got.GetAsset().GetSn() != "DRONE-1" || got.GetPath() != "live/DRONE-1/drone" ||
		!got.GetSince().AsTime().Equal(since) || got.GetViewers() != 2 {
		t.Fatalf("stream = %v", got)
	}
	if fake.request.GetContext().GetRequestId() == "" {
		t.Fatal("the request carries no request id")
	}
}

func TestListLiveStreamsWithoutAStreamIsEmpty(t *testing.T) {
	live, err := dial(t, &fakeLiveStreams{}).ListLiveStreams(timeout(t))
	if err != nil || len(live) != 0 {
		t.Fatalf("streams = %v, err = %v", live, err)
	}
}

func TestListLiveStreamsKeepsTheStatusWhenTheMediaServerDoesNotAnswer(t *testing.T) {
	fake := &fakeLiveStreams{refuse: status.Error(codes.Unavailable, "media server did not answer")}

	_, err := dial(t, fake).ListLiveStreams(timeout(t))

	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v (%v)", status.Code(err), err)
	}
}
