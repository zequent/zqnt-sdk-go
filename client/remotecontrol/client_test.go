package remotecontrol

import (
	"context"
	"net"
	"testing"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	devicecontrol "github.com/Zequent/zqnt-client-sdk-go/gen/devicecontrol/contracts/proto"
	remotecontrolpb "github.com/Zequent/zqnt-client-sdk-go/gen/remotecontrol/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeService struct {
	remotecontrolpb.UnimplementedRemoteControlServiceServer
	lastTakeOff    *devicecontrol.CoordinateCommandRequest
	lastCloseCover *devicecontrol.CloseCoverCommandRequest
}

func (s *fakeService) TakeOff(ctx context.Context, req *devicecontrol.CoordinateCommandRequest) (*devicecontrol.CommandResponse, error) {
	s.lastTakeOff = req
	return &devicecontrol.CommandResponse{Response: &devicecontrol.CommandResponse_Empty{}}, nil
}

func (s *fakeService) CloseCover(ctx context.Context, req *devicecontrol.CloseCoverCommandRequest) (*devicecontrol.CommandResponse, error) {
	s.lastCloseCover = req
	return &devicecontrol.CommandResponse{Response: &devicecontrol.CommandResponse_Empty{}}, nil
}

func (s *fakeService) GetCapabilities(ctx context.Context, req *devicecontrol.AssetCapabilitiesRequest) (*devicecontrol.AssetCapabilitiesResponse, error) {
	return &devicecontrol.AssetCapabilitiesResponse{
		Error: &base.GlobalErrorMessage{ErrorMessage: "asset offline"},
	}, nil
}

func dialFake(t *testing.T, svc remotecontrolpb.RemoteControlServiceServer) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	remotecontrolpb.RegisterRemoteControlServiceServer(server, svc)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestTakeOffSendsTheCoordinate(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	_, err := client.TakeOff(context.Background(), "drone-1", &devicecontrol.GeoCoordinate{Latitude: 52.5, Longitude: 13.4, Altitude: 30})
	if err != nil {
		t.Fatalf("TakeOff: %v", err)
	}
	if fake.lastTakeOff.GetBase().GetSn() != "drone-1" {
		t.Fatalf("expected sn to be sent, got %q", fake.lastTakeOff.GetBase().GetSn())
	}
	if fake.lastTakeOff.GetCoordinate().GetAltitude() != 30 {
		t.Fatalf("expected altitude 30, got %v", fake.lastTakeOff.GetCoordinate().GetAltitude())
	}
}

func TestCloseCoverSendsForce(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	_, err := client.CloseCover(context.Background(), "dock-1", true)
	if err != nil {
		t.Fatalf("CloseCover: %v", err)
	}
	if !fake.lastCloseCover.GetForce() {
		t.Fatalf("expected force=true to be sent")
	}
}

func TestGetCapabilitiesReturnsAnErrorOnAServerReportedFailure(t *testing.T) {
	fake := &fakeService{}
	client := dialFake(t, fake)

	_, err := client.GetCapabilities(context.Background(), "drone-offline")
	if err == nil {
		t.Fatalf("expected an error, got none")
	}
}
