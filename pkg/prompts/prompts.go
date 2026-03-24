package prompts

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterPrompts registers all MCP prompts on the server.
func RegisterPrompts(addPrompt func(mcp.Prompt, server.PromptHandlerFunc)) {
	addPrompt(
		mcp.NewPrompt("debug_service_errors",
			mcp.WithPromptDescription("Investigate errors for a service — searches error logs, aggregates error traces, and lists top operations."),
			mcp.WithArgument("service", mcp.ArgumentDescription("Service name to investigate"), mcp.RequiredArgument()),
			mcp.WithArgument("timeRange", mcp.ArgumentDescription("Time range to search (e.g., '1h', '6h', '24h'). Defaults to '1h'.")),
		),
		handleDebugServiceErrors,
	)

	addPrompt(
		mcp.NewPrompt("latency_analysis",
			mcp.WithPromptDescription("Analyze p99 latency for a service — queries latency metrics, aggregates trace durations, and identifies slow operations."),
			mcp.WithArgument("service", mcp.ArgumentDescription("Service name to analyze"), mcp.RequiredArgument()),
			mcp.WithArgument("timeRange", mcp.ArgumentDescription("Time range to analyze (e.g., '1h', '6h', '24h'). Defaults to '1h'.")),
		),
		handleLatencyAnalysis,
	)

	addPrompt(
		mcp.NewPrompt("compare_metrics",
			mcp.WithPromptDescription("Compare a metric across two time periods to identify changes or regressions."),
			mcp.WithArgument("metricName", mcp.ArgumentDescription("Name of the metric to compare"), mcp.RequiredArgument()),
			mcp.WithArgument("period1", mcp.ArgumentDescription("First time period (e.g., '24h ago to 12h ago')"), mcp.RequiredArgument()),
			mcp.WithArgument("period2", mcp.ArgumentDescription("Second time period (e.g., 'last 12h')"), mcp.RequiredArgument()),
		),
		handleCompareMetrics,
	)

	addPrompt(
		mcp.NewPrompt("incident_triage",
			mcp.WithPromptDescription("Triage an active alert — fetches alert details, history, related logs, and traces."),
			mcp.WithArgument("alertId", mcp.ArgumentDescription("Alert rule ID to triage"), mcp.RequiredArgument()),
		),
		handleIncidentTriage,
	)
}

func handleDebugServiceErrors(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	service := req.Params.Arguments["service"]
	timeRange := req.Params.Arguments["timeRange"]
	if timeRange == "" {
		timeRange = "1h"
	}

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Debug errors for %s", service),
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf(`Investigate errors for the service "%s" over the last %s. Follow these steps:

1. Use signoz_search_logs with service="%s" and severity="ERROR" and timeRange="%s" to find recent error logs.
2. Use signoz_aggregate_traces with error="true", service="%s", aggregation="count", groupBy="name", timeRange="%s" to see which operations are failing.
3. Use signoz_get_service_top_operations with service="%s" to understand the service's operation landscape.
4. Summarize: what errors are occurring, which operations are affected, and what the likely root cause is.`, service, timeRange, service, timeRange, service, timeRange, service),
				},
			},
		},
	}, nil
}

func handleLatencyAnalysis(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	service := req.Params.Arguments["service"]
	timeRange := req.Params.Arguments["timeRange"]
	if timeRange == "" {
		timeRange = "1h"
	}

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Latency analysis for %s", service),
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf(`Analyze p99 latency for the service "%s" over the last %s. Follow these steps:

1. Use signoz_aggregate_traces with service="%s", aggregation="p99", aggregateOn="durationNano", groupBy="name", timeRange="%s" to find the slowest operations.
2. Use signoz_aggregate_traces with service="%s", aggregation="p99", aggregateOn="durationNano", requestType="time_series", timeRange="%s" to see how latency has changed over time.
3. Use signoz_search_traces with service="%s", minDuration="1000000000", timeRange="%s" to find specific slow traces (>1s).
4. For the slowest trace found, use signoz_get_trace_details to examine the span breakdown.
5. Summarize: which operations are slow, whether latency is trending up, and what spans contribute most to latency.`, service, timeRange, service, timeRange, service, timeRange, service, timeRange),
				},
			},
		},
	}, nil
}

func handleCompareMetrics(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	metricName := req.Params.Arguments["metricName"]
	period1 := req.Params.Arguments["period1"]
	period2 := req.Params.Arguments["period2"]

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Compare %s across two periods", metricName),
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf(`Compare the metric "%s" across two time periods to identify changes.

Period 1: %s
Period 2: %s

Steps:
1. Use signoz_list_metrics with searchText="%s" to confirm the metric exists and get its type.
2. Use signoz_query_metrics to query the metric for period 1.
3. Use signoz_query_metrics to query the metric for period 2.
4. Compare the values and summarize: did the metric increase, decrease, or stay stable? Are there any anomalies?`, metricName, period1, period2, metricName),
				},
			},
		},
	}, nil
}

func handleIncidentTriage(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	alertID := req.Params.Arguments["alertId"]

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Triage alert %s", alertID),
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf(`Triage the alert with rule ID "%s". Follow these steps:

1. Use signoz_get_alert with ruleId="%s" to get the alert configuration and understand what it monitors.
2. Use signoz_get_alert_history with ruleId="%s" and timeRange="6h" to see when it started firing.
3. Based on the alert's signal type:
   - If logs-based: use signoz_search_logs to find related error logs around the alert trigger time.
   - If traces-based: use signoz_aggregate_traces to analyze error rates or latency around the trigger time.
   - If metrics-based: use signoz_query_metrics to see the metric trend around the trigger time.
4. Summarize: what triggered the alert, when it started, what the current state is, and recommended next steps.`, alertID, alertID, alertID),
				},
			},
		},
	}, nil
}
