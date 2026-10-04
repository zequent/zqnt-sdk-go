package connector

import (
	"context"
	"io"
	"log/slog"
	"testing"

	commonpb "github.com/zequent/zqnt-utils-golang/v2/gen/common/proto"
	connectorpb "github.com/zequent/zqnt-utils-golang/v2/gen/connector/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// deregisterStub records what DeregisterAsset was asked; every other method is the nil interface
// and panics if called.
type deregisterStub struct {
	connectorpb.ConnectorServiceClient
	asked    string
	response *connectorpb.ConnectorResponse
}

func (s *deregisterStub) DeregisterAsset(_ context.Context, in *commonpb.RequestBase, _ ...grpc.CallOption) (*connectorpb.ConnectorResponse, error) {
	s.asked = in.GetSn()
	return s.response, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Connector deletes by the request's own sn: before, the SDK sent "", deleted nothing, and said so
// with (false, nil).
func TestDeRegisterAssetSendsTheSNAndReportsARefusal(t *testing.T) {
	stub := &deregisterStub{response: &connectorpb.ConnectorResponse{}}
	svc := NewServiceImpl(stub, quiet())
	ok, err := svc.DeRegisterAsset(context.Background(), "SIM-AUTO-001")
	if err != nil || !ok || stub.asked != "SIM-AUTO-001" {
		t.Fatalf("ok=%v err=%v asked for %q, want the SN sent and success", ok, err, stub.asked)
	}

	stub.response = &connectorpb.ConnectorResponse{HasErrors: proto.Bool(true)}
	if ok, err := svc.DeRegisterAsset(context.Background(), "SIM-AUTO-001"); ok || err == nil {
		t.Fatal("a refused delete must come back as an error, not (false, nil)")
	}
	if _, err := svc.DeRegisterAsset(context.Background(), ""); err == nil {
		t.Fatal("an empty sn must be refused before it reaches connector")
	}
}
