package dashboard

import (
	"strings"
	"testing"
)

func TestWidgetsInstructionsRouteEveryQueryTypeToResources(t *testing.T) {
	for _, resourceURI := range []string{
		"signoz://dashboard/query-builder-example",
		"signoz://dashboard/clickhouse-schema-for-logs",
		"signoz://dashboard/clickhouse-logs-example",
		"signoz://dashboard/clickhouse-schema-for-metrics",
		"signoz://dashboard/clickhouse-metrics-example",
		"signoz://dashboard/clickhouse-schema-for-traces",
		"signoz://dashboard/clickhouse-traces-example",
		"signoz://promql/instructions",
	} {
		if !strings.Contains(WidgetsInstructions, resourceURI) {
			t.Errorf("widget instructions missing query resource route %q", resourceURI)
		}
	}
}
