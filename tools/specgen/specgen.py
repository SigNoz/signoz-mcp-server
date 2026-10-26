#!/usr/bin/env python3
"""Derive the spec-owned MCP surfaces from the pinned SigNoz OpenAPI document.

`make gen` runs this after the skaff client generation. Everything it writes is
generated output: regenerate it, never hand-edit it.

Outputs, relative to the repository root:

- internal/handler/tools/schemas/dashboard_{create,update,patch}.json: the v2
  (Perses) dashboard input schemas served verbatim through WithRawInputSchema.
  Each is the transitive $ref closure of its root component schema rewritten
  into a self-contained JSON Schema ($defs), with OAS 3.0 `nullable` converted
  to null unions, discriminated unions pinned so plain JSON Schema validators
  see them as mutually exclusive, and the MCP envelope fields (searchContext,
  the id parameter) injected. The MCP server is a pass-through: SigNoz is the
  only validator.
- pkg/<area>/examples.generated.md: the request examples the spec publishes on
  each mutating operation, rendered as Markdown and embedded into the
  signoz:// examples resources. The notification-channel file also carries a
  per-kind reference of config.spec fields, the only place the open `config`
  object is described.

Usage:
    specgen.py --openapi docs/api/openapi.yml --spec-ref v0.0.0 --repo-root .
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections.abc import Iterable

import yaml

SEARCH_CONTEXT = {
    "type": "string",
    "description": (
        "Copy the user's entire original request verbatim, including any preflight "
        "or confirmation context; do not summarize, shorten, or omit clauses."
    ),
}

SCHEMA_PREFIX = "#/components/schemas/"
DEFS_PREFIX = "#/$defs/"

# OAS-only keywords that are not valid JSON Schema 2020-12 vocabulary.
OAS_ONLY_KEYWORDS = ("xml", "externalDocs", "nullable", "example")


class Spec:
    def __init__(self, path: str, ref: str):
        with open(path, encoding="utf-8") as fh:
            self.doc = yaml.safe_load(fh)
        self.ref = ref
        self.schemas: dict = self.doc["components"]["schemas"]

    # ---- JSON Schema extraction -------------------------------------------------

    def closure(self, root: str) -> set[str]:
        seen: set[str] = set()
        frontier = {root}
        while frontier:
            name = frontier.pop()
            if name in seen:
                continue
            seen.add(name)
            found: set[str] = set()
            walk_refs(self.schemas[name], found)
            frontier |= found - seen
        return seen

    def build_defs(self, root: str) -> dict:
        names = self.closure(root)
        names.discard(root)  # the root is inlined at the top level
        defs = {name: rewrite_schema(self.schemas[name]) for name in sorted(names)}
        pin_discriminators(defs)
        return defs

    def root_schema(self, root: str) -> dict:
        return rewrite_schema(self.schemas[root])

    # ---- examples ---------------------------------------------------------------

    def operation(self, path: str, method: str) -> dict:
        return self.doc["paths"][path][method]

    def request_examples(self, path: str, method: str) -> dict:
        op = self.operation(path, method)
        media = op.get("requestBody", {}).get("content", {}).get("application/json", {})
        examples = media.get("examples")
        if not examples:
            raise SystemExit(f"{method.upper()} {path} publishes no request examples in the spec")
        return examples


def walk_refs(node, found: set[str]) -> None:
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "$ref" and isinstance(value, str) and value.startswith(SCHEMA_PREFIX):
                found.add(value[len(SCHEMA_PREFIX):])
            else:
                walk_refs(value, found)
    elif isinstance(node, list):
        for item in node:
            walk_refs(item, found)


def rewrite_ref(value: str) -> str:
    if isinstance(value, str) and value.startswith(SCHEMA_PREFIX):
        return DEFS_PREFIX + value[len(SCHEMA_PREFIX):]
    return value


def rewrite_schema(node):
    """Rewrite one schema node: OAS refs to $defs refs, nullable to null unions,
    OAS-only keywords dropped. Maps keyed by user-chosen names (properties,
    discriminator mappings) are rewritten by value, never filtered by key."""
    if isinstance(node, list):
        return [rewrite_schema(item) for item in node]
    if not isinstance(node, dict):
        return node
    out: dict = {}
    nullable = bool(node.get("nullable"))
    for key, value in node.items():
        if key in OAS_ONLY_KEYWORDS:
            continue
        if key == "$ref":
            out[key] = rewrite_ref(value)
        elif key in ("properties", "patternProperties", "$defs"):
            out[key] = {name: rewrite_schema(sub) for name, sub in value.items()}
        elif key == "discriminator":
            disc = dict(value)
            if isinstance(disc.get("mapping"), dict):
                disc["mapping"] = {k: rewrite_ref(v) for k, v in disc["mapping"].items()}
            out[key] = disc
        elif key in ("enum", "required", "const", "default", "examples"):
            out[key] = value
        else:
            out[key] = rewrite_schema(value)
    if nullable:
        typ = out.get("type")
        if isinstance(typ, str):
            out["type"] = [typ, "null"]
        elif isinstance(typ, list):
            if "null" not in typ:
                out["type"] = typ + ["null"]
        elif "$ref" in out:
            out = {"oneOf": [{"$ref": out.pop("$ref")}, {"type": "null"}], **out}
        elif "allOf" in out or "oneOf" in out or "anyOf" in out:
            out = {"anyOf": [dict(out), {"type": "null"}]}
    return out


def pin_discriminators(defs: dict) -> None:
    """Make OAS discriminated `oneOf` unions valid under plain JSON Schema.

    JSON Schema validators ignore `discriminator` and evaluate `oneOf` as
    exactly-one-match. Branches that type the discriminator with the full
    shared enum would match several at once, so each mapped branch pins its
    discriminator property to a single-value const and requires it, which is
    what the discriminator documents. Idempotent for branches upstream already
    narrowed (the plugin `kind` unions)."""
    for node in defs.values():
        if not (isinstance(node, dict) and "oneOf" in node and isinstance(node.get("discriminator"), dict)):
            continue
        disc = node["discriminator"]
        prop = disc.get("propertyName")
        mapping = disc.get("mapping")
        if not prop or not isinstance(mapping, dict):
            continue
        for value, ref in mapping.items():
            if not (isinstance(ref, str) and ref.startswith(DEFS_PREFIX)):
                continue
            branch = defs.get(ref[len(DEFS_PREFIX):])
            if not isinstance(branch, dict):
                continue
            branch.setdefault("properties", {})[prop] = {"type": "string", "const": value}
            required = branch.setdefault("required", [])
            if prop not in required:
                required.append(prop)


def assert_no_oas_refs(doc, label: str) -> None:
    bad = set(re.findall(r"#/components/schemas/\w+", json.dumps(doc)))
    if bad:
        raise SystemExit(f"{label}: unresolved OAS refs remain: {sorted(bad)}")


def dashboard_schemas(spec: Spec) -> dict[str, dict]:
    draft = "https://json-schema.org/draft/2020-12/schema"

    root = spec.root_schema("DashboardtypesPostableDashboardV2")
    create = {"$schema": draft, "type": "object"}
    create.update({k: v for k, v in root.items() if k != "type"})
    create.setdefault("properties", {})["searchContext"] = SEARCH_CONTEXT
    create["$defs"] = spec.build_defs("DashboardtypesPostableDashboardV2")

    # Canonical `id` is advertised and not schema-required: the handler
    # validates presence. No `uuid` alias is generated.
    uroot = spec.root_schema("DashboardtypesUpdatableDashboardV2")
    update = {"$schema": draft, "type": "object", "properties": {}, "required": []}
    update["properties"]["id"] = {"type": "string", "description": "Dashboard id (UUID) to update."}
    update["properties"].update(uroot.get("properties", {}))
    update["required"] = list(uroot.get("required", []))
    update["properties"]["searchContext"] = SEARCH_CONTEXT
    update["$defs"] = spec.build_defs("DashboardtypesUpdatableDashboardV2")

    proot = spec.root_schema("DashboardtypesPatchableDashboardV2")
    patch = {
        "$schema": draft,
        "type": "object",
        "properties": {
            "id": {"type": "string", "description": "Dashboard id (UUID) to patch."},
            "patch": proot,
            "searchContext": SEARCH_CONTEXT,
        },
        "required": ["patch"],
        "$defs": spec.build_defs("DashboardtypesPatchableDashboardV2"),
    }

    out = {"create": create, "update": update, "patch": patch}
    for name, doc in out.items():
        assert_no_oas_refs(doc, name)
    return out


# ---- examples rendering -----------------------------------------------------------


def render_examples(spec: Spec, title: str, operations: Iterable[tuple[str, str, str]], extra: str = "") -> str:
    """Render the request examples of the given operations as Markdown.

    operations: (method, path, heading) triples. Every example becomes an H3
    with its upstream key and summary, its description when the spec carries
    one, and the request body as a JSON block."""
    lines = [
        f"<!-- Generated by tools/specgen/specgen.py from SigNoz docs/api/openapi.yml at {spec.ref}. Do not edit. -->",
        "",
        f"# {title}",
        "",
        f"Source: request examples published by the SigNoz OpenAPI document at {spec.ref}.",
        "",
    ]
    if extra:
        lines += [extra.rstrip("\n"), ""]
    for method, path, heading in operations:
        examples = spec.request_examples(path, method)
        lines += [f"## {heading} ({method.upper()} {path})", ""]
        for index, (key, example) in enumerate(examples.items(), start=1):
            summary = example.get("summary") or key
            lines += [f"### {index}. {key} — {summary}", ""]
            description = (example.get("description") or "").strip()
            if description:
                lines += [description, ""]
            body = json.dumps(example.get("value"), indent=2, ensure_ascii=False)
            lines += ["```json", body, "```", ""]
    return "\n".join(lines).rstrip("\n") + "\n"


def schema_type(node: dict) -> str:
    if "$ref" in node:
        return node["$ref"].rsplit("/", 1)[1]
    if "allOf" in node and len(node["allOf"]) == 1 and "$ref" in node["allOf"][0]:
        return node["allOf"][0]["$ref"].rsplit("/", 1)[1]
    typ = node.get("type", "object")
    if node.get("format") == "password":
        return "string (secret)"
    if typ == "array":
        return f"array of {schema_type(node.get('items', {}))}"
    if "enum" in node:
        return "one of " + ", ".join(str(v) for v in node["enum"])
    return typ


def render_fields(spec: Spec, schema: dict, indent: int, rendered: set[str]) -> list[str]:
    """One bullet per property: name, type, required marker, description.
    Referenced object schemas are expanded once, nested under the field."""
    lines: list[str] = []
    required = set(schema.get("required", []))
    pad = "  " * indent
    for name, prop in sorted(schema.get("properties", {}).items()):
        typ = schema_type(prop)
        marker = " (required)" if name in required else ""
        description = (prop.get("description") or "").strip()
        lines.append(f"{pad}- `{name}` {typ}{marker}: {description}".rstrip(": "))
        ref = None
        if "$ref" in prop:
            ref = prop["$ref"]
        elif "allOf" in prop and len(prop["allOf"]) == 1:
            ref = prop["allOf"][0].get("$ref")
        elif prop.get("type") == "array" and "$ref" in prop.get("items", {}):
            ref = prop["items"]["$ref"]
        if ref and ref.startswith(SCHEMA_PREFIX):
            target = ref[len(SCHEMA_PREFIX):]
            sub = spec.schemas[target]
            if sub.get("type") == "object" and "properties" in sub and target not in rendered:
                rendered.add(target)
                lines += render_fields(spec, sub, indent + 1, rendered)
    return lines


def render_channel_kinds(spec: Spec) -> str:
    union = spec.schemas["AlertmanagertypesChannelConfig"]
    mapping = union["discriminator"]["mapping"]
    lines = [
        "## config.spec fields by kind",
        "",
        "`config` is `{kind, spec}`. The kinds below are every value SigNoz accepts at this "
        "revision; `spec` fields marked required must be present. Fields marked secret are "
        "returned by create, update and get by ID, never by list.",
        "",
    ]
    for kind in sorted(mapping):
        variant = spec.schemas[mapping[kind][len(SCHEMA_PREFIX):]]
        spec_prop = variant["properties"]["spec"]
        target = schema_type(spec_prop)
        config = spec.schemas[target]
        lines += [f"### {kind}", ""]
        lines += render_fields(spec, config, 0, {target})
        lines.append("")
    return "\n".join(lines).rstrip("\n") + "\n"


# ---- main -------------------------------------------------------------------------


def write(path: str, content: str) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(content)
    print(f"[specgen] wrote {path} ({len(content.encode('utf-8'))} bytes)")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--openapi", required=True, help="path to the pinned docs/api/openapi.yml")
    parser.add_argument("--spec-ref", required=True, help="the SigNoz git ref the spec was fetched from")
    parser.add_argument("--repo-root", default=".", help="repository root to write into")
    args = parser.parse_args()

    spec = Spec(args.openapi, args.spec_ref)
    root = os.path.abspath(args.repo_root)

    for name, doc in dashboard_schemas(spec).items():
        path = os.path.join(root, "internal", "handler", "tools", "schemas", f"dashboard_{name}.json")
        write(path, json.dumps(doc, indent=2, ensure_ascii=False) + "\n")

    write(
        os.path.join(root, "pkg", "alert", "examples.generated.md"),
        render_examples(spec, "Alert rule request examples", [("post", "/api/v2/rules", "Create or replace a rule")]),
    )
    write(
        os.path.join(root, "pkg", "views", "examples.generated.md"),
        render_examples(
            spec,
            "Saved view request examples",
            [
                ("post", "/api/v2/saved_views", "Create a view"),
                ("put", "/api/v2/saved_views/{id}", "Replace a view"),
            ],
        ),
    )
    write(
        os.path.join(root, "pkg", "dashboard", "examples.generated.md"),
        render_examples(
            spec,
            "Dashboard request examples",
            [
                ("post", "/api/v2/dashboards", "Create a dashboard"),
                ("put", "/api/v2/dashboards/{id}", "Replace a dashboard"),
                ("patch", "/api/v2/dashboards/{id}", "Patch a dashboard"),
            ],
        ),
    )
    write(
        os.path.join(root, "pkg", "notificationchannel", "examples.generated.md"),
        render_examples(
            spec,
            "Notification channel request examples",
            [
                ("post", "/api/v2/notification_channels", "Create a channel"),
                ("put", "/api/v2/notification_channels/{id}", "Replace a channel's config"),
            ],
            extra=render_channel_kinds(spec),
        ),
    )
    write(
        os.path.join(root, "pkg", "querybuilder", "examples.generated.md"),
        render_examples(
            spec,
            "Query range request examples",
            [("post", "/api/v5/query_range", "Run a query")],
        ),
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
