package dashboard

// PatchInstructions is the recipe guide served at signoz://dashboard/patch-instructions:
// worked, server-verified RFC 6902 JSON Patch operations for targeted dashboard edits
// (add/edit/move/remove a panel, edit a query, rename, variables, tags) with the exact
// JSON Pointer paths and the multi-op sequences the backend requires.
const PatchInstructions = `
Recipes for signoz_patch_dashboard (RFC 6902 JSON Patch): targeted edits to a v2 dashboard without re-sending the whole payload. Every "patch" example below is an array of {op, path, value} operations verified against a live SigNoz instance. For the JSON of a panel/query/variable value, read signoz://dashboard/widgets-examples and signoz://dashboard/instructions; for the exact field set use the create/update tool's JSON Schema.

Shape recap (JSON Pointers target the postable shape):
- spec.panels is a MAP keyed by panel id: {"<id>": {"kind":"Panel","spec":{...}}}.
- spec.layouts is an ARRAY of Grid envelopes: [{"kind":"Grid","spec":{"items":[ {"x","y","width","height","content":{"$ref":"#/spec/panels/<id>"}} ]}}]. The grid item positions a panel; its content.$ref links it to the panels map. New dashboards already have one Grid at layouts[0]; grid items are 0-based in spec.layouts[0].spec.items.
- spec.variables and /tags are arrays; /spec/display holds name/description.

Critical [read first]:
- Adding a panel takes TWO ops in ONE patch: add the panel at /spec/panels/<id> AND append a grid item at /spec/layouts/0/spec/items/- whose content.$ref is "#/spec/panels/<id>". A panel with no grid item is accepted but never renders.
- Removing a panel also takes TWO ops in ONE patch: remove its grid item AND remove the panel entry. The whole dashboard is validated after the ops apply, so a grid item whose $ref points to a removed panel is rejected ("references unknown panel").
- The top-level "name" is immutable — never patch /name. Rename via replace /spec/display/name (this does NOT change the immutable name).
- A panel holds exactly ONE query (backend-enforced). To combine series, replace the single query with a signoz/CompositeQuery — do NOT append a second entry to /spec/panels/<id>/spec/queries.
- Apply is lenient (remove on a missing path is a no-op; add creates missing parents) but the final dashboard is still validated; locked dashboards are rejected.

--- Recipes (each block is the "patch" array) ---

Rename / edit description:
[{"op":"replace","path":"/spec/display/name","value":"New title"}]
[{"op":"replace","path":"/spec/display/description","value":"..."}]

Add a panel (panel + grid item together). Set the grid item's "y" below every existing item (its y + height) — overlapping items are rejected ("items overlap"):
[
  {"op":"add","path":"/spec/panels/panel-b","value":{ ...a Panel — copy a shape from signoz://dashboard/widgets-examples... }},
  {"op":"add","path":"/spec/layouts/0/spec/items/-","value":{"x":0,"y":6,"width":12,"height":6,"content":{"$ref":"#/spec/panels/panel-b"}}}
]

Edit one field of an existing panel (e.g. its title or y-axis unit):
[{"op":"replace","path":"/spec/panels/panel-b/spec/display/name","value":"Renamed panel"}]

Replace a panel's query (one query per panel — replace, don't append; use signoz/CompositeQuery for multi-series):
[{"op":"replace","path":"/spec/panels/panel-b/spec/queries/0","value":{ ...a query — see signoz://dashboard/widgets-examples... }}]

Move / resize a panel (edit its grid item):
[{"op":"replace","path":"/spec/layouts/0/spec/items/0","value":{"x":6,"y":0,"width":6,"height":6,"content":{"$ref":"#/spec/panels/panel-a"}}}]

Add a variable (appends even when variables is empty):
[{"op":"add","path":"/spec/variables/-","value":{"kind":"ListVariable","spec":{"name":"service_name","display":{"name":"Service Name"},"plugin":{"kind":"signoz/DynamicVariable","spec":{"name":"service.name","signal":"logs"}}}}}]
(spec.name is the $service_name reference handle; plugin.spec.name is the attribute key. See signoz://dashboard/instructions for variable rules — always ask which panels a variable applies to before wiring it in.)

Add a tag:
[{"op":"add","path":"/tags/-","value":{"key":"team","value":"platform"}}]

Remove a panel (grid item + panel entry together; items are 0-based):
[
  {"op":"remove","path":"/spec/layouts/0/spec/items/1"},
  {"op":"remove","path":"/spec/panels/panel-b"}
]
`
