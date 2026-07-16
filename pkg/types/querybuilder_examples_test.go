package types

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/metricsrules"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/stretchr/testify/require"
)

var (
	plainGuideExamplePattern = regexp.MustCompile(`(?s)--- Example \d+:[^\n]*---\n\n(\{.*?\n\})\n`)
	markdownJSONPattern      = regexp.MustCompile("(?s)```json\\n(.*?)\\n```")
)

func TestQueryBuilderGuideExamplesUseExecutableBoundsContract(t *testing.T) {
	tests := []struct {
		name      string
		guide     string
		pattern   *regexp.Regexp
		wantCount int
	}{
		{name: "logs", guide: querybuilder.LogsQueryBuilderGuide, pattern: plainGuideExamplePattern, wantCount: 4},
		{name: "traces", guide: querybuilder.TracesQueryBuilderGuide, pattern: plainGuideExamplePattern, wantCount: 3},
		{name: "metrics", guide: metricsrules.MetricsGuide, pattern: markdownJSONPattern, wantCount: 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			examples := tc.pattern.FindAllStringSubmatch(tc.guide, -1)
			require.Len(t, examples, tc.wantCount, "guide JSON example count changed; update this executable contract test")
			for index, match := range examples {
				var payload QueryPayload
				require.NoErrorf(t, json.Unmarshal([]byte(match[1]), &payload), "example %d is invalid JSON", index+1)
				require.NoErrorf(t, payload.Validate(), "example %d does not satisfy QueryPayload.Validate", index+1)
				require.Emptyf(t, payload.AppliedBounds, "example %d omitted explicit limit/order and was auto-healed by validation", index+1)
				for queryIndex, query := range payload.CompositeQuery.Queries {
					switch spec := query.Spec.(type) {
					case QuerySpec:
						wantLimit := defaultLimitForRequestType(payload.RequestType)
						require.Equalf(t, wantLimit, spec.Limit, "example %d query %d should teach the request-type default limit", index+1, queryIndex+1)
						wantOrder, err := defaultOrderForQuery(spec, payload.RequestType, queryIndex)
						require.NoErrorf(t, err, "example %d query %d default order", index+1, queryIndex+1)
						require.Equalf(t, wantOrder, spec.Order, "example %d query %d should teach the signal-specific default order", index+1, queryIndex+1)
					case FormulaSpec:
						require.Equalf(t, DefaultAggregateQueryLimit, spec.Limit, "example %d query %d formula default limit", index+1, queryIndex+1)
						require.Equalf(t, resultDescendingOrder(), spec.Order, "example %d query %d formula default order", index+1, queryIndex+1)
					}
				}
			}
		})
	}
}
