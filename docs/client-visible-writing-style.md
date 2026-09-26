# Client-Visible Writing Style

Read this when you add or edit a string an MCP client or agent reads. Skip it for handler logic, tests, and internal comments.

Applies to tool and parameter descriptions, `signoz://` resource content (`pkg/alert`, `pkg/dashboard`, `pkg/views`, `pkg/promql`, `pkg/querybuilder`, `pkg/metricsrules`), prompts (`pkg/prompts`), server instructions (`pkg/instructions`), and `manifest.json` descriptions. These strings are instructions an agent executes, not prose that persuades a reader; keep them plain, concrete, and testable. Model-drafted text arrives with a recognizable set of tics; strip them before opening the PR.

- **No em dashes in prose.** Use a colon for a label gloss, a semicolon or period between independent clauses, parentheses for an aside, a comma for brief apposition. Two separator uses stay allowed: fixed-width table columns in guides (`has_error  bool  — whether the span has an error`) and markdown headings (`## 3. metric_promql — PromQL rule`).
- **Banned words.** delve, foster, leverage, utilize, facilitate, empower, streamline, robust, seamless, cutting-edge, paradigm shift, game changer, transformative, elevate, embark, supercharge, harness, ever-evolving, meticulous, intricate, paramount, realm, tapestry, beacon, multifaceted. Say the plain thing instead: "use", "cut", "reliable".
- **Cut empty phrases.** it's worth noting, it's important to note, at the end of the day, when it comes to, at its core, in today's world, the reality is, in order to, going forward. Delete them and state the instruction.
- **No AI-slop rhetoric.** Skip binary contrasts ("it's not X, it's Y"), faux-insight setups ("what most people miss"), colon reveals ("the detail that makes it work: ..."), importance puffery ("plays a vital role"), weasel attribution ("experts agree"), rhetorical questions, and summary-recap endings. Make the claim directly. Plain label colons ("One of: ...", "Example: ...") are fine.
- **No punchy fragments, aphorisms, or templated lead-ins.** State rules as rules. "Note: This panel is best used when..." repeated across five panels became "Best for: ...".
- **No superficial `-ing` analysis.** Drop trailing "highlighting X", "revealing Y", "making it Z" clauses. State the mechanism or the consequence.
- **Be concrete.** Name the tool, field, default, or limit. "Are capped at the top 100" beats "use top 100"; "operations ranked by p99 latency" beats "operation landscape".
- **Active voice, direct verbs.** "The server rejects a partial body", not "a partial body is rejected".
- **Formatting follows content.** No emoji in headings, no bold sprinkled mid-sentence for emphasis, no bullet list where two sentences read better.
- **Keep concept handles verbatim.** Phrases like "same still-current prepared operation" and "full replacement" are anchored across tool descriptions, server instructions, and guardrail tests. Reword a handle everywhere at once or not at all; reordering around a pinned handle is fine.
- **Out of scope.** Strings generated from upstream stay as generated: the dashboard input schemas (`internal/handler/tools/schemas/`, from the SigNoz OpenAPI spec) and `dashboard_templates.json` titles.

Wording changes to these strings are MCP contract changes: sync the wire-catalog fixtures (mirror textual edits; recompute derived `size`/`serializedLength`/`sha256` through the oracle helpers, never re-record the catalog) and stay within the guardrail byte budgets. Reviewers should treat any pattern above in a diff as a change request; `grep -n '—'` over changed files is the fastest single check.
