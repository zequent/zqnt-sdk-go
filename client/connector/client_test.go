package connector

import (
	"context"
	"net"
	"testing"

	base "github.com/Zequent/zqnt-client-sdk-go/gen/common/base/proto"
	connectorpb "github.com/Zequent/zqnt-client-sdk-go/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeConnectorService struct {
	connectorpb.UnimplementedConnectorServiceServer
}

func dialFake(t *testing.T, svc connectorpb.ConnectorServiceServer) *Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	connectorpb.RegisterConnectorServiceServer(server, svc)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestGetAssetBySnReturnsAnErrorOnAServerReportedFailure(t *testing.T) {
	fake := &erroringConnectorService{message: "asset not found"}
	client := dialFake(t, fake)

	_, err := client.GetAssetBySn(context.Background(), "unknown")
	if err == nil {
		t.Fatalf("expected an error, got none")
	}
}

type erroringConnectorService struct {
	connectorpb.UnimplementedConnectorServiceServer
	message string
}

func (s *erroringConnectorService) GetAssetBySn(ctx context.Context, req *base.RequestBase) (*connectorpb.ConnectorResponse, error) {
	hasErrors := true
	return &connectorpb.ConnectorResponse{HasErrors: &hasErrors,
		Response: &connectorpb.ConnectorResponse_Error{Error: &base.GlobalErrorMessage{ErrorMessage: s.message}}}, nil
}
