package dashboard

import _ "embed"

// generatedExamples is rendered by tools/specgen/specgen.py from the request
// examples SigNoz publishes on the dashboard operations in the pinned OpenAPI
// spec: complete create bodies, a full replacement, and JSON Patch arrays.
//
//go:embed examples.generated.md
var generatedExamples string

// DashboardExamples is the complete-dashboard example guide served at
// signoz://dashboard/examples. It complements signoz://dashboard/widgets-examples,
// which shows single panels in isolation.
var DashboardExamples = examplesPreamble + generatedExamples

const examplesPreamble = `Complete v6 (Perses) dashboard payloads. The create examples are signoz_create_dashboard arguments, the replace example is a signoz_update_dashboard body (add "id"), and the patch examples are the "patch" array of signoz_patch_dashboard. Copy the structure, not the literal metric and attribute names: discover the target workspace's fields with signoz_get_field_keys first. For single-panel structure and choosing a panel or query type, read signoz://dashboard/widgets-examples and signoz://dashboard/widgets-instructions; for layout and variable rules read signoz://dashboard/instructions.

Create envelope: {schemaVersion:"v6", generateName:true, tags:[...], spec:{display, variables, panels, layouts, links:[]}}. Do not set the top-level "name" on create (generateName lets the server derive it); send it unchanged on a full replacement.

Metrics Query Builder temporality: omit it. SigNoz resolves cumulative, delta or unspecified from the metric's metadata; set it only to override that, since a wrong value returns no data.

Metrics Query Builder ordering: the order key of a grouped or aggregated metrics query is the composed aggregation expression spaceAggregation(timeAggregation(metricName)), for example sum(rate(system.cpu.time)), or __result; the bare metric name is rejected.

`
