# Released-feature contract migration

This release requires SigNoz v0.142.0 or newer. Update the server and companion
agent skills together. It does not retain the previous notification-channel
API, parameter aliases on changed tools, or older-backend fallbacks.

## Notification channels

Use `config: {kind, spec}` for create and update. Provider fields use the v2
camelCase schema, including `sendResolved`; flat fields such as `type`,
`send_resolved`, and `slack_api_url` are rejected. Omitted `sendResolved` uses
the provider default rather than forcing true.

`id` identifies a stored channel. `name` is its immutable machine name;
`displayName` is its immutable routing name. Alert thresholds and anomaly
`preferredChannels` must use the returned `displayName`.

For updates, get the channel, copy its complete `config`, edit the requested
fields, and submit `id` plus that config. Preserve credentials without echoing
them. A full replacement can change the provider kind but cannot rename the
channel. Empty optional template strings returned by SigNoz are normalized
back to unset during update. List results omit credentials and expose filtered
totals plus pagination metadata; follow `pagination.nextOffset` while
`pagination.hasMore` is true before concluding a channel is absent.

Create and update no longer send tests by default. Set `test: true` to request
a test after the write. Test failure does not undo a successful write. If a
post-write authorization failure reports `mutationCommitted: true`, use the
returned ID to inspect state after authenticating; do not repeat the mutation.
Updates that request tests are not safe to replay automatically.
`testNotification.status` reports `skipped`, `succeeded`, `failed`, or `unknown`.
A skipped test was not sent; a transport failure can leave its outcome unknown.
Success means SigNoz accepted the test request, not proof of final provider delivery.

## Dashboard and query inputs

Changed dashboard tools accept `id`, not the former `uuid` alias. Log filters
use `filter`, not `query`. `searchText` remains a body-only convenience; use
`filter: "search('timeout')"` for explicit cross-field search.

Text panels use `signoz/TextPanel` and `queries: []`; query panels still require
one query. Raw builder heatmaps preserve bucket metadata and per-point counts.
Neither heatmap dashboard panels nor saved-view heatmap rendering are advertised.

The v2 backend excludes system dashboards from list results. Get-by-ID can
still return one with its source intact. System and integration dashboards
cannot be modified, patched, or deleted by users; errors retain the shared
MCP error category. No dedicated system-dashboard tool is added.
