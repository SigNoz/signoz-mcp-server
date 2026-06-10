# Feature: Resource Web URLs — Context & Discussion

## Original Prompt
> Add an absolute deep-link (`webUrl`) field to the resource read-tool outputs so
> the AI assistant can hand users a clickable link to a dashboard, alert, or
> service in the SigNoz web UI.

## Reference Links
- Issue: [SigNoz/signoz-ai-assistant#245](https://github.com/SigNoz/signoz-ai-assistant/issues/245) (resource links)

## Key Decisions & Discussion Log
### 2026-06-10 — why and where the link is built
- The AI assistant needs an absolute deep link per resource so it can hand users a
  clickable link.
- Decision: build the link in the MCP server (single source of truth; benefits all
  MCP clients incl. Cursor/Claude Desktop which are not same-origin as SigNoz).
- Scope: dashboard, alert, service (single-id). Saved views dropped — they require
  the full encoded `compositeQuery` in the URL; there is no id-only frontend route.
- Templates:
  - `/dashboard/<uuid>`
  - `/alerts/overview?ruleId=<id>`
  - `/services/<url-encoded-name>`
- Origin comes from `util.GetSigNozURL(ctx)`; `webUrl` is omitted on an empty base.
- Helper: `pkg/util/weburl.go` `ResourceWebURL(base, type, id) -> (url, ok)`.

### 2026-06-10 — docs sync (Task A5)
- Added this plan-file pair and a README note under "Available Tools" documenting
  the new `webUrl` output field on the six resource read tools.
- `manifest.json` left unchanged: it documents tool `name`/`description` only (not
  output shapes), and no tool descriptions changed in A1–A4 — only outputs gained a
  field.

### 2026-06-10 — drop inert `tab=AlertRules` from alert webUrl (#245)
- Alert template changed from `/alerts/overview?ruleId=<id>&tab=AlertRules` to
  `/alerts/overview?ruleId=<id>`. The `tab=AlertRules` param is inert on the
  `/alerts/overview` route — that page uses `AlertDetailsTab` (OVERVIEW/HISTORY);
  `AlertRules` is a tab on the `/alerts` list page, not the overview detail page.
  Removed it so the deep link is clean.

## Open Questions
- [x] Should saved views get a `webUrl`? — No; no id-only frontend route exists
  (the URL requires the full encoded `compositeQuery`).
- [x] Where should the origin come from? — `util.GetSigNozURL(ctx)`; omit `webUrl`
  when empty.
