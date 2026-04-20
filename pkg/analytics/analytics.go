package analytics

import (
	"context"

	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
)

type Analytics interface {
	// Enabled reports whether analytics is actively emitting events.
	Enabled() bool
	// Start blocks until the analytics backend is stopped.
	Start(context.Context) error
	// Stop flushes pending events and closes the client.
	Stop(context.Context) error

	// Send sends raw analytics messages.
	Send(context.Context, ...analyticstypes.Message)

	// TrackUser tracks an event attributed to a specific user within a group.
	TrackUser(ctx context.Context, group string, user string, event string, properties map[string]any)

	// IdentifyUser sets traits on a user and associates them with a group.
	IdentifyUser(ctx context.Context, group string, user string, traits map[string]any)
}
