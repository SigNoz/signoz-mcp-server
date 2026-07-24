package dashboard

const ListFilterGuide = `
Dashboard list filter — the "filter" argument of signoz_list_dashboards

This is an optional, server-side filter over dashboard METADATA; it narrows which dashboards the list returns. It does not search panel or query contents. Omit it to list every dashboard.

Grammar
- A filter is a boolean expression of terms.
- A term is either "key operator value" (a comparison) or a bare word (free text).
- Combine terms with AND, OR, NOT and parentheses. Precedence: parentheses, then NOT, then AND, then OR. Two terms with no operator between them are an implicit AND, so "prod payment" means "prod AND payment".
- Free text: a bare word is a case-insensitive substring match over the dashboard name, description, and tag keys/values. Quote a phrase to match it literally, e.g. "prod payment", or to search text that would otherwise parse as DSL, e.g. "team = prod".

Filterable keys
There are two kinds of keys: a fixed set of reserved keys (dashboard columns) and tag keys (anything else). The list response echoes the authoritative reserved-key set in its reservedKeywords field, so read that if unsure.

Reserved keys and the operators each accepts:
- name, description, created_by  (string): =, !=, LIKE, NOT LIKE, ILIKE, NOT ILIKE, CONTAINS, NOT CONTAINS, IN, NOT IN
- created_at, updated_at  (RFC3339 timestamp): =, !=, <, <=, >, >=, BETWEEN, NOT BETWEEN
- locked  (boolean): =, !=

Tag keys: any identifier that is not a reserved key is treated as a tag key (matched case-insensitively). Tag keys accept: =, !=, LIKE, NOT LIKE, ILIKE, NOT ILIKE, CONTAINS, NOT CONTAINS, IN, NOT IN, EXISTS, NOT EXISTS. Use EXISTS / NOT EXISTS to assert a tag with that key is present or absent.

Values
- Strings: quote with single or double quotes, e.g. 'frontend' or "frontend". A single bare word is also accepted, e.g. env = prod.
- Timestamps: quoted RFC3339, e.g. '2026-03-10T00:00:00Z'. Offsets are honored, e.g. '2026-03-10T05:30:00+05:30'.
- Booleans: bare true or false (case-insensitive). locked = 'yes' is an error.
- IN lists: a bracketed or parenthesized list, e.g. team IN ['pulse', 'events'].

Operator notes
- LIKE / ILIKE expect you to supply % wildcards yourself, e.g. name LIKE 'Prod%'. ILIKE is the case-insensitive form.
- CONTAINS wraps the value in %...% for you (and escapes literal %), so name CONTAINS 'overview' matches any name containing "overview".
- REGEXP / NOT REGEXP are NOT supported for dashboard listing; they are rejected. Use LIKE / ILIKE / CONTAINS instead.
- EXISTS / NOT EXISTS apply only to tag keys.
- An operator outside a key's allowed set is rejected (e.g. name BETWEEN ..., or locked LIKE ...).

Limits
- The filter string is capped at 1024 characters.

Examples
- name = 'overview'
- name CONTAINS 'overview'
- name ILIKE 'prod%'
- created_by LIKE '%@signoz.io'
- locked = true
- created_at >= '2026-03-10T00:00:00Z'
- updated_at BETWEEN '2026-03-10T00:00:00Z' AND '2026-03-20T00:00:00Z'
- team = 'pulse'
- team IN ['pulse', 'events']
- database EXISTS
- locked = true AND created_by = 'a@b.com'
- (locked = true OR locked = false) AND created_by = 'a@b.com'
- NOT team = 'pulse' AND name = 'overview'
- name CONTAINS 'overview' AND created_by = 'naman.verma@signoz.io' AND database = 'mongo'

Tips
- To find a dashboard by a word in its title, description, or tags, just pass that word as free text.
- If a filter returns nothing unexpectedly, drop clauses one at a time; a misspelled tag key becomes a tag filter that matches nothing rather than an error.
`
