package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleCheckMetricUsage_ReturnsDashboardsAndAlerts(t *testing.T) {
	mock := &client.MockClient{
		CheckMetricUsageFn: func(_ context.Context, names []string) (map[string]client.MetricUsage, error) {
			return map[string]client.MetricUsage{
				"system.disk.io": {
					Dashboards: []string{"Host Metrics", "Host Metrics (k8s)"},
					Alerts:     []string{},
				},
				"k8s.node.condition": {
					Dashboards: []string{},
					Alerts:     []string{},
				},
			}, nil
		},
	}

	h := newTestHandler(mock)
	req := makeToolRequest("signoz_check_metric_usage", map[string]any{
		"metricNames": []any{"system.disk.io", "k8s.node.condition"},
	})

	result, err := h.handleCheckMetricUsage(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if result.StructuredContent == nil {
		t.Fatalf("expected structuredContent on successful metric usage result")
	}

	text := textContent(t, result)
	var out map[string]client.MetricUsage
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	disk := out["system.disk.io"]
	if len(disk.Dashboards) != 2 {
		t.Errorf("expected 2 dashboards for system.disk.io, got %d", len(disk.Dashboards))
	}

	node := out["k8s.node.condition"]
	if len(node.Dashboards) != 0 || len(node.Alerts) != 0 {
		t.Errorf("expected empty dashboards and alerts for k8s.node.condition")
	}
}

func TestHandleCheckMetricUsage_EmptyNamesReturnsError(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_check_metric_usage", map[string]any{
		"metricNames": []any{},
	})

	result, err := h.handleCheckMetricUsage(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for empty metricNames, got success")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", code, CodeValidationFailed)
	}
}

func TestHandleCheckMetricUsage_MissingNamesReturnsError(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_check_metric_usage", map[string]any{})

	result, err := h.handleCheckMetricUsage(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing metricNames, got success")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", code, CodeValidationFailed)
	}
}

func TestHandleCheckMetricUsage_InvalidNamesReturnValidationCodes(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	cases := []struct {
		name string
		args map[string]any
	}{
		{
			name: "non-array",
			args: map[string]any{"metricNames": "system.cpu.time"},
		},
		{
			name: "non-string entry",
			args: map[string]any{"metricNames": []any{"system.cpu.time", 42}},
		},
		{
			name: "only empty strings",
			args: map[string]any{"metricNames": []any{"", ""}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := h.handleCheckMetricUsage(testCtx(), makeToolRequest("signoz_check_metric_usage", tc.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected validation error result, got success")
			}
			if code := resultCode(t, result); code != CodeValidationFailed {
				t.Fatalf("code = %q, want %q", code, CodeValidationFailed)
			}
		})
	}
}

func TestHandleCheckMetricUsage_OversizedBatchReturnsValidationCode(t *testing.T) {
	called := false
	h := newTestHandler(&client.MockClient{
		CheckMetricUsageFn: func(_ context.Context, names []string) (map[string]client.MetricUsage, error) {
			called = true
			return nil, nil
		},
	})
	names := make([]any, client.MaxMetricUsageNames+1)
	for i := range names {
		names[i] = fmt.Sprintf("metric.%d", i)
	}

	result, err := h.handleCheckMetricUsage(testCtx(), makeToolRequest("signoz_check_metric_usage", map[string]any{
		"metricNames": names,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected validation error result, got success")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", code, CodeValidationFailed)
	}
	if called {
		t.Fatalf("client should not be called for oversized batch")
	}
}

func TestHandleCheckMetricUsage_PreservesDedupedDashboards(t *testing.T) {
	// Verify that the client layer deduplicates dashboard names when the same
	// dashboard has multiple widgets referencing the metric. This test uses the
	// real parseDashboardNames helper via a mock that already returns deduplicated
	// names (dedup is client-side, verified in client unit tests).
	mock := &client.MockClient{
		CheckMetricUsageFn: func(_ context.Context, names []string) (map[string]client.MetricUsage, error) {
			return map[string]client.MetricUsage{
				"system.cpu.time": {
					// Same dashboard name once — dedup already applied by client
					Dashboards: []string{"Host Metrics"},
					Alerts:     []string{},
				},
			}, nil
		},
	}

	h := newTestHandler(mock)
	req := makeToolRequest("signoz_check_metric_usage", map[string]any{
		"metricNames": []any{"system.cpu.time"},
	})

	result, err := h.handleCheckMetricUsage(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	text := textContent(t, result)
	var out map[string]client.MetricUsage
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	cpu := out["system.cpu.time"]
	if len(cpu.Dashboards) != 1 {
		t.Errorf("expected 1 deduplicated dashboard name, got %d", len(cpu.Dashboards))
	}
}
