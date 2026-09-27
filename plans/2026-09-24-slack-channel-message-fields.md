# Plan: Slack Channel Message Fields

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/319

## Context

SigNoz v0.143.0 (source `7ce73f3470371daa7b245716a1b1a3fd6a2daee4`) adds Slack message settings to
notification channels (SigNoz#12907): `color`, `titleLink`, `pretext`, `fallback`, `footer`,
`fields`, and `actions`. GET responses now carry the five string settings as `""` when unset. The
MCP Slack schema was strict and pinned to v0.142.0, so a get-merge-update of any Slack channel on
v0.143.0 failed with `config.spec: unknown field "color"`. The e2e suite on v0.143.0 caught it in
`test_notification_channels[slack]`.

Other v0.143.0 changes reviewed: the channel repair API (SigNoz#12910) is a new optional feature and
stays out of scope; the webhook bearer parsing fix (SigNoz#12890) and the removed internal
`PromQLProvider` request field need no MCP change. The dashboard area chart panel ships in its own PR.

## Approach

Mirror the upstream Slack config in the typed spec, request validation, tool schema, the update
path's unset-string normalization, and the client readback allowlist. Keep create strict: empty
strings are rejected on create and normalized away only on update, as for the existing template
fields. Move the e2e SigNoz image to v0.143.0 so the live suite exercises the new contract.

## Files to Modify

- `pkg/types/notification_channels.go` — Slack spec fields, attachment validation, unset template fields.
- `internal/handler/tools/notification_channels_schema.go` — Slack schema properties.
- `internal/client/notification_channels.go` — readback allowlist; pinned-contract log text.
- Unit tests beside each package; `internal/mcp-server/testdata/wire-catalog/tools-list.json` for the two channel tool schemas only.
- `README.md` — Slack message settings.
- `tests/casting.yaml`, `tests/fixtures/signoz.py` — SigNoz v0.143.0 image and version gate; collector v0.144.11.
- `tests/fixtures/foundry.py` — wait for the ClickHouse migrator before tests start.
- `tests/e2e/tests/test_notification_channels.py` — Slack message settings round trip.

## Key Decisions

### 2026-09-24 — Validate attachments like upstream

- Decision: require `title` and `value` on fields, `type` and `text` on actions, `url` or `name` on each action, and `text` on a confirmation.
- Rationale: these are the rules `ChannelSlackConfig.Validate` enforces, so the tool rejects invalid input with a field-level message before a write.

### 2026-09-24 — Collector v0.144.11 and a migration wait

- Decision: pin the e2e collector to v0.144.11 and wait for the telemetry-store migrator to exit 0 after the port check.
- Rationale: SigNoz v0.143.0 pushes an OpAMP config with the `signozspanmapper` and `signozllmpricing` processors, which collector v0.142.0 rejects, so it never opened the OTLP ports (14 fixture timeouts). The upgrade guide requires v0.144.11. With OTLP fixed, three tests raced the ClickHouse migrations (`Unknown table ... distributed_column_evolution_metadata`) because SigNoz answers on 8080 before the schema exists.

## Reference Links

- [SigNoz#12907: more Slack configuration options](https://github.com/SigNoz/signoz/pull/12907)
- [SigNoz v0.143.0 upgrade guide](https://signoz.io/docs/operate/migration/upgrade-0-143/)
- [SigNoz v0.143.0 release](https://github.com/SigNoz/signoz/releases/tag/v0.143.0)

## Verification

- `GOTOOLCHAIN=go1.26.0 make ci` passes: typed-spec and validation tests, the update normalization test with a v0.143.0 GET echo, the pinned readback shapes, and the wire catalog for the two channel tool schemas.
- `make test-e2e` against a clean SigNoz v0.143.0 cast with collector v0.144.11: 59 passed, 0 failed (run with the stacked area chart PR), including `test_every_provider_config_round_trips_and_update_preserves_it[slack]` and the new `test_slack_message_settings_round_trip_through_get_and_full_update`.

## Outcome

Slack channels round-trip their v0.143.0 message settings through create, get, and full-config update, and e2e runs against SigNoz v0.143.0. The channel repair API (SigNoz#12910) is not covered.
