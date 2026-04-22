# Plan: Adopt SigNoz v2 convention for rules and notification channels

## Status
In Progress

## Context
Upstream SigNoz has moved alert rules to `/api/v2/rules/*` (PR #10957), introduced canonical `createdAt`/`createdBy` audit fields on a v2 `ruletypes.Rule` (PR #10997), tightened Postable/Gettable schemas (PR #10995), and similarly re-homed notification-channel routes onto the render envelope (PR #10941 — added for scope). This PR brings the MCP server's alert, channel, and new downtime tools onto the new convention.

## Approach

### Client layer (`internal/client/`)
- Switch `GetAlertByRuleID` and `CreateAlertRule` URLs to `/api/v2/rules[/{id}]`.
- Add `UpdateAlertRule(ctx, ruleID, alertJSON)` → PUT `/api/v2/rules/{id}` (204).
- Add `DeleteAlertRule(ctx, ruleID)` → DELETE `/api/v2/rules/{id}` (204).
- Add `GetNotificationChannel(ctx, id)` → GET `/api/v1/channels/{id}`.
- Add `DeleteNotificationChannel(ctx, id)` → DELETE `/api/v1/channels/{id}` (204).
- Repoint `TestNotificationChannel` URL to `/api/v1/channels/test`.
- Mirror all additions in `interface.go` and `mock.go`.
- Downtime schedule tools are **out of scope** (removed after initial prototype per follow-up direction).

### Helpers
- New `pkg/uuidv7/uuidv7.go` with `IsUUIDv7(string) bool` for client-side rule-ID validation.

### Handlers (`internal/handler/tools/`)
- `alerts.go`:
  - Register `signoz_update_alert` and `signoz_delete_alert`.
  - Update handlers validate UUID-v7 before the client call; `update` reuses the `alert.ValidateFromMap` pipeline and the existing channel-reference validation.
- `notification_channels.go`:
  - Adjust `handleListNotificationChannels` to read `name` from the top-level Channel field (#10941 keeps `Name` at the top; the MCP code was going through the stringified `data.name`, which still works but is now redundant).
  - Adjust `handleUpdateNotificationChannel` to treat an empty 204 body as success (don't try to unmarshal).
  - Register new `signoz_get_notification_channel` and `signoz_delete_notification_channel`.

### Docs/metadata
- `manifest.json`: add/update tool entries (new IDs + descriptions).
- `README.md`: tool tables refreshed; note required SigNoz version ≥ the release shipping PRs #10941 + #10957 + #10995 + #10997.

### Tests
- `alerts_test.go`: refresh fixtures to the v2 shape where practical; add update/delete coverage.
- `notification_channels_test.go`: fixtures adjusted for render envelope; add get/delete.

## Files to Modify
- `internal/client/client.go` — URL swaps + new channel/alert methods
- `internal/client/interface.go` — interface additions
- `internal/client/mock.go` — mock additions
- `internal/handler/tools/alerts.go` — v2 URLs + new update/delete handlers
- `internal/handler/tools/notification_channels.go` — envelope/status code fixes + new get/delete handlers
- `internal/mcp-server/server.go` — (notification channel handler registration already present)
- `internal/handler/tools/alerts_test.go`, `notification_channels_test.go`
- `pkg/util/uuid.go` — **new** UUIDv7 validator
- `manifest.json`, `README.md`

## Verification
1. `go build ./...`
2. `go test ./...`
3. Manual smoke against a SigNoz with all four PRs merged:
   - Create alert via `signoz_create_alert` → expect 201, response has `createdAt`/`createdBy` under canonical names.
   - Get, update, delete — with a bad UUID the client refuses; with a valid one delete returns no error.
   - Channel CRUD incl. test step via `/api/v1/channels/test`.
