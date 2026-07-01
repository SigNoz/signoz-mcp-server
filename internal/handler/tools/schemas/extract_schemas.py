#!/usr/bin/env python3
"""
Generator for the embedded dashboard input schemas in this directory
(dashboard_create.json / dashboard_update.json / dashboard_patch.json).

It takes the three v2 (Perses) root schemas from SigNoz's OpenAPI spec, computes
the transitive $ref closure of each, rewrites the OAS refs into self-contained
JSON Schema ($defs), converts OAS 3.0 `nullable: true` into JSON-Schema null
unions, and injects the top-level `searchContext` property. The Perses plugin
`oneOf`/discriminator unions are preserved (struct reflection can't express them).

The MCP server is a pass-through: these schemas are served to clients verbatim
via WithRawInputSchema, and the v2 API is the authoritative validator. Regenerate
whenever the upstream OpenAPI dashboard schemas change.

USAGE (the recipe used to produce the committed files):
    # 1. fetch the upstream spec to the hardcoded input path
    curl -sL https://raw.githubusercontent.com/SigNoz/signoz/main/docs/api/openapi.yml \
        -o /tmp/openapi.yml
    # 2. run this script -> writes /tmp/dash_schemas/{create,update,patch}.json
    pip3 install pyyaml   # if needed
    python3 extract_schemas.py
    # 3. copy into place with the dashboard_ prefix
    cp /tmp/dash_schemas/create.json internal/handler/tools/schemas/dashboard_create.json
    cp /tmp/dash_schemas/update.json internal/handler/tools/schemas/dashboard_update.json
    cp /tmp/dash_schemas/patch.json  internal/handler/tools/schemas/dashboard_patch.json

NOTE: input (/tmp/openapi.yml) and output (/tmp/dash_schemas/) paths are hardcoded
below; adjust if you want a different location. The core extraction is the
verified one-off run that produced the current committed schemas; the K5 id/uuid
handling on update/patch (canonical `id` + `uuid` alias, neither required) was
originally applied as manual edits to the JSON afterward and is now folded into
this script so a regen reproduces the committed files end-to-end.
"""
import yaml, json, os, re

with open('/tmp/openapi.yml') as f:
    spec = yaml.safe_load(f)

schemas = spec['components']['schemas']

def walk_refs(node, found):
    if isinstance(node, dict):
        for k, v in node.items():
            if k == '$ref' and isinstance(v, str) and v.startswith('#/components/schemas/'):
                found.add(v.rsplit('/', 1)[1])
            else:
                walk_refs(v, found)
    elif isinstance(node, list):
        for item in node:
            walk_refs(item, found)

def closure(root_name):
    seen = set()
    frontier = {root_name}
    while frontier:
        n = frontier.pop()
        if n in seen: continue
        seen.add(n)
        nxt = set()
        walk_refs(schemas[n], nxt)
        frontier |= (nxt - seen)
    return seen

def rewrite_refs(node):
    """Rewrite #/components/schemas/X -> #/$defs/X and convert OAS3.0 nullable -> JSON Schema."""
    if isinstance(node, dict):
        out = {}
        nullable = node.pop('nullable', None) if 'nullable' in node else None
        for k, v in node.items():
            if k == '$ref' and isinstance(v, str) and v.startswith('#/components/schemas/'):
                out[k] = '#/$defs/' + v.rsplit('/', 1)[1]
            elif k in ('xml', 'externalDocs', 'discriminator') and k != 'discriminator':
                continue  # drop OAS-only keywords not valid in JSON Schema (keep discriminator below)
            else:
                out[k] = rewrite_refs(v)
        # handle discriminator mapping ref rewrite
        if 'discriminator' in out and isinstance(out['discriminator'], dict):
            disc = out['discriminator']
            if 'mapping' in disc and isinstance(disc['mapping'], dict):
                disc['mapping'] = {mk: ('#/$defs/' + mv.rsplit('/',1)[1] if isinstance(mv,str) and mv.startswith('#/components/schemas/') else mv) for mk, mv in disc['mapping'].items()}
        # nullable conversion
        if nullable:
            t = out.get('type')
            if isinstance(t, str):
                out['type'] = [t, 'null']
            elif t is None and ('$ref' in out or 'oneOf' in out or 'allOf' in out or 'anyOf' in out):
                # wrap ref-only nullable as oneOf with null
                ref = out.pop('$ref', None)
                if ref:
                    out = {'oneOf': [{'$ref': ref}, {'type': 'null'}]}
        return out
    elif isinstance(node, list):
        return [rewrite_refs(x) for x in node]
    return node

def pin_discriminators(defs):
    """Make OAS discriminated `oneOf` unions valid under plain JSON Schema.

    OpenAPI relies on the `discriminator` keyword to pick a branch, but JSON
    Schema validators ignore `discriminator` and evaluate `oneOf` as
    exactly-one-match. When the branches are not mutually exclusive on their own
    (e.g. Querybuildertypesv5QueryEnvelope, whose branches all type their
    discriminator property with the *full* shared enum), a valid payload matches
    several branches and `oneOf` fails. For every discriminated union we pin each
    mapped branch's discriminator property to a single-value `const` (and require
    it), which is exactly what the discriminator documents — restoring mutual
    exclusivity. This is idempotent for branches upstream already narrowed
    (the plugin `kind` unions), and it is what makes the CompositeQuery examples
    validate against the emitted schema.
    """
    for node in defs.values():
        if not (isinstance(node, dict) and 'oneOf' in node and isinstance(node.get('discriminator'), dict)):
            continue
        disc = node['discriminator']
        prop = disc.get('propertyName')
        mapping = disc.get('mapping')
        if not prop or not isinstance(mapping, dict):
            continue
        for mapkey, ref in mapping.items():
            if not (isinstance(ref, str) and ref.startswith('#/$defs/')):
                continue
            branch = defs.get(ref.rsplit('/', 1)[1])
            if not isinstance(branch, dict):
                continue
            branch.setdefault('properties', {})[prop] = {"type": "string", "const": mapkey}
            req = branch.setdefault('required', [])
            if prop not in req:
                req.append(prop)

def build_defs(root_name):
    names = closure(root_name)
    names.discard(root_name)  # root inlined at top level; deps in $defs
    defs = {}
    for n in sorted(names):
        defs[n] = rewrite_refs(schemas[n])
    pin_discriminators(defs)
    return defs

SEARCH_CTX = {"type": "string", "description": "The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results."}

def assert_no_oas_refs(doc, label):
    s = json.dumps(doc)
    bad = re.findall(r'#/components/schemas/\w+', s)
    if bad:
        raise SystemExit(f"{label}: unresolved OAS refs remain: {set(bad)}")

os.makedirs('/tmp/dash_schemas', exist_ok=True)
reports = {}

# ---- create: PostableDashboardV2 inlined + searchContext ----
root = rewrite_refs(schemas['DashboardtypesPostableDashboardV2'])
defs = build_defs('DashboardtypesPostableDashboardV2')
create = {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}
create.update({k: v for k, v in root.items() if k not in ('type',)})
create.setdefault('properties', {})['searchContext'] = SEARCH_CTX
create['$defs'] = defs
assert_no_oas_refs(create, 'create')
json.dump(create, open('/tmp/dash_schemas/create.json','w'), indent=2)
reports['create'] = (root.get('required', []), list(create['properties'].keys()), len(defs))

# ---- update: id + UpdatableDashboardV2 props inlined + searchContext ----
uroot = rewrite_refs(schemas['DashboardtypesUpdatableDashboardV2'])
udefs = build_defs('DashboardtypesUpdatableDashboardV2')
update = {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": {}, "required": []}
# K5 contract: canonical `id` + permanent `uuid` alias, with NEITHER schema-required
# (a resource id must never be required — TestUpdateStructs_IDNotSchemaRequired).
update['properties']['id'] = {"type": "string", "description": "Dashboard id (UUID) to update."}
update['properties']['uuid'] = {"type": "string", "description": "Legacy alias for id; accepted for backward compatibility."}
for k, v in uroot.get('properties', {}).items():
    update['properties'][k] = v
for r in uroot.get('required', []):
    if r not in update['required']:
        update['required'].append(r)
update['properties']['searchContext'] = SEARCH_CTX
update['$defs'] = udefs
assert_no_oas_refs(update, 'update')
json.dump(update, open('/tmp/dash_schemas/update.json','w'), indent=2)
reports['update'] = (update['required'], list(update['properties'].keys()), len(udefs))

# ---- patch: id + patch(PatchableDashboardV2) + searchContext ----
proot = rewrite_refs(schemas['DashboardtypesPatchableDashboardV2'])
pdefs = build_defs('DashboardtypesPatchableDashboardV2')
# K5 contract: `id` + `uuid` alias, neither required (only `patch` is required).
patch = {"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
         "properties": {
             "id": {"type": "string", "description": "Dashboard id (UUID) to patch."},
             "uuid": {"type": "string", "description": "Legacy alias for id; accepted for backward compatibility."},
             "patch": proot,
             "searchContext": SEARCH_CTX,
         },
         "required": ["patch"],
         "$defs": pdefs}
assert_no_oas_refs(patch, 'patch')
json.dump(patch, open('/tmp/dash_schemas/patch.json','w'), indent=2)
reports['patch'] = (patch['required'], list(patch['properties'].keys()), len(pdefs))

print("=== OpenAPI 3.0.3; nullable->JSON Schema conversion applied ===")
for name, (req, props, ndefs) in reports.items():
    sz = os.path.getsize(f'/tmp/dash_schemas/{name}.json')
    print(f"\n[{name}.json]  {sz} bytes, {ndefs} $defs")
    print(f"  required: {req}")
    print(f"  top props: {props}")
