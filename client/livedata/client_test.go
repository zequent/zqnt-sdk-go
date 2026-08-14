package livedata

import (
	"context"
	"net"
	"testing"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	devicecontrol "github.com/Zequent/zqnt-client-sdk-go/gen/devicecontrol/contracts/proto"
	livedatapb "github.com/Zequent/zqnt-client-sdk-go/gen/livedata/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeService struct {
	livedatapb.UnimplementedLiveDataServiceServer
	lastStartRecording *devicecontrol.EmptyCommandRequest
}

func (s *fakeService) StartRecording(ctx context.Context, req *devicecontrol.EmptyCommandRequest) (*devicecontrol.CommandResponse, error) {
	s.lastStartRecording = req
	return &devicecontrol.CommandResponse{Response: &devicecontrol.CommandResponse_Empty{}}, nil
}

func (s *fakeService) ChangeZoom(ctx context.Context, req *devicecontrol.ChangeCameraZoomCommandRequest) (*devicecontrol.CommandResponse, error) {
	hasErrors := true
	return &devicecontrol.CommandResponse{
		HasErrors: &hasErrors,
		Response:  &devicecontrol.CommandResponse_Error{Error: &base.GlobalErrorMessage{ErrorMessage: "camera not ready"}},
	}, nil
}

func dialFake(t *testing.T, svc livedatapb.LiveDataServiceServer) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	livedatapb.RegisterLiveDataServiceServer(server, svc)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestStartRecordingSendsTheSn(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	_, err := client.StartRecording(context.Background(), "cam-1")
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if fake.lastStartRecording.GetBase().GetSn() != "cam-1" {
		t.Fatalf("expected sn to be sent, got %q", fake.lastStartRecording.GetBase().GetSn())
	}
}

func TestChangeZoomReturnsAnErrorOnAServerReportedFailure(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	_, err := client.ChangeZoom(context.Background(), &devicecontrol.ChangeCameraZoomCommandRequest{Base: &base.RequestBase{Sn: "cam-1"}})
	if err == nil {
		t.Fatalf("expected an error, got none")
	}
}
