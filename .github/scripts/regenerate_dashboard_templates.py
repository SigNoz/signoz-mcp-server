#!/usr/bin/env python3
"""Regenerate the bundled dashboard_templates.json from the upstream repo.

Usage:
    python3 .github/scripts/regenerate_dashboard_templates.py

Walks the SigNoz/dashboards repo at the tip of `main`, locates each template
JSON, extracts the title/description (v6 `spec.display.*`, falling back to v1
top-level fields) and tags, and emits the bundled index at
internal/handler/tools/dashboard_templates.json.

The runtime fetcher (signoz_import_dashboard) also reads from `main`, so the
catalog and the fetcher always reference the same ref. There is no pinned
SHA to keep in sync.

Adapted from
https://github.com/SigNoz/agent-skills/blob/main/plugins/signoz/skills/signoz-dashboard-create/scripts/regenerate_index.py
(the upstream version pins a SHA; this one tracks main).

This is a maintenance tool. It is not invoked at runtime. Hits the public
GitHub API; rerun with GITHUB_TOKEN set if rate-limited.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path
from typing import Any
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import Request, urlopen

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_OUTPUT = REPO_ROOT / "internal" / "handler" / "tools" / "dashboard_templates.json"

UPSTREAM_REF = "main"
GITHUB_TREE_URL = "https://api.github.com/repos/SigNoz/dashboards/git/trees/{ref}?recursive=1"
GITHUB_RAW_URL = "https://raw.githubusercontent.com/SigNoz/dashboards/{ref}/{path}"


def _http_get(url: str) -> bytes:
    req = Request(url)
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Accept", "application/vnd.github+json")
    with urlopen(req, timeout=60) as response:
        body: bytes = response.read()
        return body


def _list_template_paths(ref: str) -> list[str]:
    body = _http_get(GITHUB_TREE_URL.format(ref=ref))
    tree = json.loads(body)
    paths: list[str] = []
    for node in tree.get("tree", []):
        if node.get("type") != "blob":
            continue
        path = node.get("path", "")
        if not path.endswith(".json"):
            continue
        parts = path.split("/")
        if len(parts) != 2:
            continue
        if parts[1].startswith("."):
            continue
        paths.append(path)
    return sorted(paths)


def _tokenize(text: str) -> list[str]:
    return [t for t in re.split(r"[\s/_\-\.]+", text.lower()) if t and len(t) > 1]


def _derive_keywords(path: str, title: str, tags: list[str]) -> list[str]:
    tokens: list[str] = []
    tokens.extend(_tokenize(path))
    tokens.extend(_tokenize(title))
    for tag in tags:
        tokens.extend(_tokenize(tag))

    seen: set[str] = set()
    unique: list[str] = []
    stopwords = {"dashboard", "json", "template"}
    for token in tokens:
        if token in stopwords or token in seen:
            continue
        seen.add(token)
        unique.append(token)
    return unique


def _derive_category(path: str) -> str:
    first = path.split("/", 1)[0]
    return first.replace("-", " ").title()


def _build_entry(ref: str, path: str) -> dict[str, Any] | None:
    body = _http_get(GITHUB_RAW_URL.format(ref=ref, path=quote(path)))
    try:
        data = json.loads(body)
    except json.JSONDecodeError:
        return None

    # v6 puts the human title/description under spec.display; fall back to the
    # v1 top-level fields so the catalog regenerates cleanly through the
    # migration, then to the filename.
    spec = data.get("spec") or {}
    display = spec.get("display") or {}
    title = display.get("name") or data.get("title") or path.rsplit("/", 1)[-1].removesuffix(".json")
    description = display.get("description") or data.get("description") or ""

    # Tags feed the keyword index only (the entry has no tags field). v6 tags
    # are {key, value} objects, v1 tags are plain strings; flatten both to text
    # so _derive_keywords can tokenize them, indexing key AND value for v6.
    raw_tags = data.get("tags")
    tags: list[str] = []
    if isinstance(raw_tags, list):
        for t in raw_tags:
            if isinstance(t, dict):
                tags.extend(str(v) for v in (t.get("key"), t.get("value")) if v)
            elif isinstance(t, str):
                tags.append(t)

    return {
        "id": path.split("/", 1)[0],
        "title": title,
        "path": path,
        "keywords": _derive_keywords(path, title, tags),
        "description": description,
        "category": _derive_category(path),
    }


def regenerate(ref: str, output: Path) -> int:
    paths = _list_template_paths(ref)
    if not paths:
        sys.stderr.write("No template paths found in upstream tree\n")
        return 1

    entries: list[dict[str, Any]] = []
    for path in paths:
        try:
            entry = _build_entry(ref, path)
        except HTTPError as exc:
            sys.stderr.write(f"skip {path}: HTTP {exc.code}\n")
            continue
        if entry is None:
            sys.stderr.write(f"skip {path}: not valid JSON\n")
            continue
        entries.append(entry)

    entries.sort(key=lambda e: (e["category"], e["title"].lower()))
    output.write_text(
        json.dumps(entries, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    sys.stderr.write(f"Wrote {len(entries)} entries to {output}\n")
    return 0


def _parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--output",
        type=Path,
        default=DEFAULT_OUTPUT,
        help="Output dashboard_templates.json path",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv if argv is not None else sys.argv[1:])
    return regenerate(UPSTREAM_REF, args.output)


if __name__ == "__main__":
    raise SystemExit(main())
