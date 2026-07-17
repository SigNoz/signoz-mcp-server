package tools

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// Composite annotation options, one per tool class. Every registered tool must
// use exactly one of these so the full readOnly/destructive/idempotent triple
// is always set explicitly; annotations_inventory_test.go pins the advertised
// triple per tool. Per the MCP spec, destructiveHint means "may destroy or
// overwrite existing data" — additive creates are therefore not destructive.

// withReadOnlyToolAnnotations marks a tool that never modifies the SigNoz
// backend. Safe for clients to auto-approve and retry.
func withReadOnlyToolAnnotations() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(true)(t)
		mcp.WithDestructiveHintAnnotation(false)(t)
		mcp.WithIdempotentHintAnnotation(true)(t)
	}
}

// withCreateToolAnnotations marks a tool that adds a new resource. Additive,
// so not destructive; repeating the call creates a duplicate, so not
// idempotent.
func withCreateToolAnnotations() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(false)(t)
		mcp.WithDestructiveHintAnnotation(false)(t)
		mcp.WithIdempotentHintAnnotation(false)(t)
	}
}

// withUpdateToolAnnotations marks a tool that overwrites an existing resource
// via full-replacement PUT: destructive (replaces prior state), idempotent
// (repeating the same payload converges to the same state).
func withUpdateToolAnnotations() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(false)(t)
		mcp.WithDestructiveHintAnnotation(true)(t)
		mcp.WithIdempotentHintAnnotation(true)(t)
	}
}

// withNonIdempotentUpdateToolAnnotations marks an update tool whose handler
// performs an external side effect on every call beyond the PUT itself —
// signoz_update_notification_channel sends a live test notification after
// each successful update — so a repeat call re-notifies and is not free.
func withNonIdempotentUpdateToolAnnotations() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(false)(t)
		mcp.WithDestructiveHintAnnotation(true)(t)
		mcp.WithIdempotentHintAnnotation(false)(t)
	}
}

// withDeleteToolAnnotations marks a tool that removes a resource by id:
// destructive, and idempotent in the HTTP DELETE sense — a repeat call fails
// with not-found upstream but causes no additional state change.
func withDeleteToolAnnotations() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(false)(t)
		mcp.WithDestructiveHintAnnotation(true)(t)
		mcp.WithIdempotentHintAnnotation(true)(t)
	}
}
