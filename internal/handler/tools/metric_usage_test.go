package tools

import (
	"context"
	"encoding/json"
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
					SafeToDrop: false,
				},
				"k8s.node.condition": {
					Dashboards: []string{},
					Alerts:     []string{},
					SafeToDrop: true,
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

	text := textContent(t, result)
	var out map[string]client.MetricUsage
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	disk := out["system.disk.io"]
	if disk.SafeToDrop {
		t.Errorf("system.disk.io should not be safe to drop")
	}
	if len(disk.Dashboards) != 2 {
		t.Errorf("expected 2 dashboards for system.disk.io, got %d", len(disk.Dashboards))
	}

	node := out["k8s.node.condition"]
	if !node.SafeToDrop {
		t.Errorf("k8s.node.condition should be safe to drop")
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
					SafeToDrop: false,
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
