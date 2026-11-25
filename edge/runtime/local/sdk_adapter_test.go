package local_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"

	edgesdk "github.com/zequent/zqnt-sdk-go/v3/edge"
	sdkadapter "github.com/zequent/zqnt-sdk-go/v3/edge/adapter"
	"github.com/zequent/zqnt-sdk-go/v3/edge/adapter/domains"
	"github.com/zequent/zqnt-sdk-go/v3/edge/runtime/local"
)

// An adapter built with this SDK attaches by configuration only: its platform endpoint is the node
// socket, and it serves on the socket the node knows as its default adapter.
func TestAnSDKAdapterAttachesToTheNodeSocket(t *testing.T) {
	dir := socketDir(t)
	adapterSock := filepath.Join(dir, "adapter.sock")
	n := startNode(t, local.Config{DefaultAdapterEndpoint: "unix://" + adapterSock})

	var takeoffs atomic.Int32
	dock := &struct{ sdkadapter.Base }{}
	dock.MustRegisterCommand("flight.takeoff", nil, nil, func(context.Context, *domains.CustomCommandRequest) (*domains.CommandResult, error) {
		takeoffs.Add(1)
		return domains.Success("airborne", asset), nil
	})
	client, err := edgesdk.NewEdgeClient(n.conn.Target(), asset, dock, edgesdk.WithoutPlatformAuth(),
		edgesdk.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	lis, err := local.Listen(adapterSock, local.ListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.StartServing(ctx, lis) }()
	defer func() { _ = client.Shutdown(context.Background()) }()

	waitFor(t, "the adapter's capabilities", func() bool {
		for _, env := range n.rec.all() {
			if env.GetCapabilities().GetAssetSn() == asset {
				return true
			}
		}
		return false
	})
	n.deliver(t, "c-1", takeoff(asset))
	waitFor(t, "the outcome", func() bool { return len(n.rec.states("c-1")) == 2 })
	equalStates(t, n.rec.states("c-1"), accepted, succeeded)
	if takeoffs.Load() != 1 {
		t.Fatalf("takeoff ran %d times", takeoffs.Load())
	}
}
