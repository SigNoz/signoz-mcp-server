package analytics

import (
	"context"

	"github.com/SigNoz/signoz-mcp-server/pkg/types/analyticstypes"
)

type Analytics interface {
	// Start blocks until the analytics backend is stopped.
	Start(context.Context) error
	// Stop flushes pending events and closes the client.
	Stop(context.Context) error

	// Send sends raw analytics messages.
	Send(context.Context, ...analyticstypes.Message)

	// TrackGroup tracks an event at the organization/group level.
	// The group value is used directly as the userId and associated via Context.Extra groupId.
	TrackGroup(ctx context.Context, group string, event string, properties map[string]any)

	// TrackUser tracks an event attributed to a specific user within a group.
	TrackUser(ctx context.Context, group string, user string, event string, properties map[string]any)

	// IdentifyGroup sets traits on an organization/group.
	IdentifyGroup(ctx context.Context, group string, traits map[string]any)

	// IdentifyUser sets traits on a user and associates them with a group.
	IdentifyUser(ctx context.Context, group string, user string, traits map[string]any)
}
