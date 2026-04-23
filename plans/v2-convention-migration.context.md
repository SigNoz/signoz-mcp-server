# Feature: Adopt SigNoz v2 convention for rules, channels, and downtime — Context & Discussion

## Original Prompt
> Based on the PRs at
> https://github.com/SigNoz/signoz/pull/10998
> https://github.com/SigNoz/signoz/pull/10997
> https://github.com/SigNoz/signoz/pull/10995
> https://github.com/SigNoz/signoz/pull/10957
> Convert the existing create_alert and notification channel tools and other affected tools to the new convention

## Reference Links
- [SigNoz PR #10957 — ruler handlers → signozapiserver](https://github.com/SigNoz/signoz/pull/10957)
- [SigNoz PR #10995 — tighten rule/downtime OpenAPI schema](https://github.com/SigNoz/signoz/pull/10995)
- [SigNoz PR #10997 — v2 Rule read type + UUID validation](https://github.com/SigNoz/signoz/pull/10997)
- [SigNoz PR #10998 — docs: core-type + Postable/Gettable/Storable conventions](https://github.com/SigNoz/signoz/pull/10998)
- [SigNoz PR #10941 — alertmanager handlers → signozapiserver](https://github.com/SigNoz/signoz/pull/10941) — the notification-channel equivalent of #10957

## Key Decisions & Discussion Log

### 2026-04-22 — Initial planning
- PR scope audit: of the 4 PRs in the prompt, only #10957 + #10995 + #10997 touch rules and downtime; #10998 is docs-only. None of the four touch notification channels — PR #10941 (merged 2026-04-15) is the channel equivalent and is included in scope by user direction.
- **Alert CRUD**: migrate existing `signoz_get_alert` + `signoz_create_alert` to `/api/v2/rules/*`. Add new `signoz_update_alert` (PUT) + `signoz_delete_alert` (DELETE). `signoz_get_alert_history` stays on `/api/v1/rules/{id}/history/timeline` because the `history/timeline` route is not migrated upstream (only `history/filter_keys` is on v2). `signoz_list_alerts` is unaffected — it calls `/api/v1/alerts` (Alertmanager), not `/rules`.
- **Channels**: migrate all three existing tools to the render envelope on `/api/v1/channels/*` (paths unchanged, status codes shift to 201/204). Add new `signoz_get_notification_channel` (GET `/api/v1/channels/{id}`) and `signoz_delete_notification_channel` (DELETE `/api/v1/channels/{id}`). Switch the test step from the deprecated `/api/v1/testChannel` to the new canonical `/api/v1/channels/test`.
- **Downtime schedules**: add a full tool set — list/get/create/update/delete — against `/api/v1/downtime_schedules[/*]`.
- **Version compatibility**: v2-only on the MCP side; accept the hard break for older SigNoz deployments. Will document minimum SigNoz version in README.
- **204 No Content bodies**: `doRequest` already returns empty bytes on 204 (treated as success because status is 2xx). Handlers that issue PUT/DELETE must not try to `json.Unmarshal` an empty body.
- **UUID-v7 validation**: server enforces it on DELETE `/api/v2/rules/{id}`; we'll validate client-side in both `signoz_update_alert` and `signoz_delete_alert` to surface a clearer error before the round-trip.
- **Downtime schedule input**: the `schedule` subtree (recurrence, cumulative window, timezone) is complex; MCP tool accepts it as a raw JSON object and relies on the server's tightened validation (post-#10995) to reject bad input. Keeps the tool surface maintainable.
- **Alert ID selection in downtime tools**: per the "never auto-select notification channels" user rule (applied here by analogy), the `alertIds` field is passed through verbatim — the tool must not try to resolve them from names. Users supply IDs from `signoz_get_alert`.

### 2026-04-22 — Drop downtime schedule tools from scope
- User directed: remove the downtime / planned-maintenance tools (list/get/create/update/delete) and every trace of them — client methods, interface entries, mock entries, handler file, tests, manifest/README references, and the client-layer helpers only used by them (the `strconv` import reverts to unused and is removed).
- Integration test tool count returns from 35 to 30 (5 removed). Notification-channel migration remains in scope.
- Downtime schedule support is deferred entirely; not even a read-only list tool remains.

## Open Questions
- [ ] Minimum SigNoz version that ships PRs #10941/#10957/#10995/#10997 — need to fill into README once it's released.
- [ ] Should MCP-side tightening mirror PR #10995's enum constraints (e.g. `alertType` must be one of four literals), or leave validation entirely to the server? Current lean is: server is the source of truth; MCP stays permissive.
