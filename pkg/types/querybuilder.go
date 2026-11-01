package types

import (
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// ExecuteBuilderQueryInput is the signoz_execute_builder_query input schema: the
// generated QueryRangeRequest under "query" plus the searchContext envelope
// field, so the schema follows the pinned SigNoz spec with no hand-kept copy.
type ExecuteBuilderQueryInput struct {
	Query         apitypes.Querybuildertypesv5QueryRangeRequest `json:"query" jsonschema:"The complete query_range request; apart from the result bounds below it reaches SigNoz verbatim and SigNoz validates it. start and end are required. requestType has no default: use raw, trace, time_series, or scalar. For predictable bounds, supply a positive spec.limit and non-empty spec.order (not dashboard orderBy) on every builder_formula and on each builder_query no formula references; the server inserts signal-aware defaults when they are omitted. Missing or zero standalone and formula-result limits normalize to 100. A builder_query a formula references gets no inserted limit or order, because SigNoz applies its limit before the formula runs; leave its limit unset."`
	SearchContext string                                        `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}
