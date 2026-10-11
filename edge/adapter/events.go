package adapter

import (
	"context"

	"github.com/zequent/zqnt-edge-sdk-go/v3/adapter/domains"
)

// CommandEventPublisher reports the progress and completion of accepted commands. The client's
// LiveData() is one; conformance.Recorder is one for tests. Hold this interface in an adapter, not
// the concrete client, so the conformance kit can check the events.
type CommandEventPublisher interface {
	PublishCommandExecutionEvent(ctx context.Context, event *domains.CommandExecutionEvent) error
}
