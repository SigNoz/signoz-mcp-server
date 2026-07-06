package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleCheckMetricCardinality_ReturnsAttributes(t *testing.T) {
	mock := &client.MockClient{
		GetMetricCardinalityFn: func(_ context.Context, name string, start, end int64) (json.RawMessage, error) {
			if name != "k8s.container.memory_limit" {
				t.Errorf("unexpected metric name: %s", name)
			}
			return json.RawMessage(`{
				"status": "success",
				"data": {
					"attributes": [
						{"key": "container.id", "valueCount": 4704, "values": ["abc123", "def456"]},
						{"key": "k8s.pod.uid", "valueCount": 591, "values": ["uid-1", "uid-2"]},
						{"key": "k8s.namespace.name", "valueCount": 4, "values": ["default", "kube-system", "monitoring", "app"]}
					],
					"totalKeys": 3
				}
			}`), nil
		},
	}

	h := newTestHandler(mock)
	req := makeToolRequest("signoz_check_metric_cardinality", map[string]any{
		"metricName": "k8s.container.memory_limit",
		"timeRange":  "7d",
	})

	result, err := h.handleCheckMetricCardinality(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	text := textContent(t, result)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data field in response")
	}
	attrs, ok := data["attributes"].([]any)
	if !ok || len(attrs) != 3 {
		t.Errorf("expected 3 attributes, got %v", data["attributes"])
	}

	// Verify highest-cardinality attribute is first
	first := attrs[0].(map[string]any)
	if first["key"] != "container.id" {
		t.Errorf("expected container.id first (highest cardinality), got %v", first["key"])
	}
	if first["valueCount"].(float64) != 4704 {
		t.Errorf("expected valueCount 4704, got %v", first["valueCount"])
	}

	// Verify values are included
	values, ok := first["values"].([]any)
	if !ok || len(values) == 0 {
		t.Error("expected values array to be present and non-empty")
	}
}

func TestHandleCheckMetricCardinality_MissingMetricNameReturnsError(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_check_metric_cardinality", map[string]any{})

	result, err := h.handleCheckMetricCardinality(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing metricName, got success")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", code, CodeValidationFailed)
	}
}

func TestHandleCheckMetricCardinality_MalformedTimestampReturnsValidationCode(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_check_metric_cardinality", map[string]any{
		"metricName": "system.cpu.time",
		"start":      "yesterday",
	})

	result, err := h.handleCheckMetricCardinality(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected validation error result for malformed start, got success")
	}
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", code, CodeValidationFailed)
	}
}

// TestHandleCheckMetricCardinality_AuthzFailureReturnsUpstreamCode pins that a
// SigNoz 401/403 propagates through the shared upstreamError path with the
// status-derived code — so clients re-authenticate rather than treat the metric
// as having no attributes. The error is wrapped exactly as the real client wraps
// it (fmt.Errorf("...: %w")) to prove errors.As classification survives wrapping.
func TestHandleCheckMetricCardinality_AuthzFailureReturnsUpstreamCode(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		wantCode   string
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, wantCode: CodeUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden, wantCode: CodePermissionDenied},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&client.MockClient{
				GetMetricCardinalityFn: func(_ context.Context, name string, _, _ int64) (json.RawMessage, error) {
					return nil, fmt.Errorf("cardinality lookup for %q: %w", name, &client.HTTPStatusError{
						StatusCode: tc.statusCode,
						Body:       `{"status":"error","error":{"code":"unauthenticated","message":"invalid credentials","type":"unauthorized"}}`,
					})
				},
			})

			result, err := h.handleCheckMetricCardinality(testCtx(), makeToolRequest("signoz_check_metric_cardinality", map[string]any{
				"metricName": "system.cpu.time",
			}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected authz error result, got success")
			}
			if code := resultCode(t, result); code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
			if got := resultStructuredMap(t, result)["status"]; got != tc.statusCode {
				t.Fatalf("status = %#v, want %d", got, tc.statusCode)
			}
		})
	}
}

func TestHandleCheckMetricCardinality_DefaultTimeRange7d(t *testing.T) {
	var capturedStart, capturedEnd int64
	mock := &client.MockClient{
		GetMetricCardinalityFn: func(_ context.Context, _ string, start, end int64) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`{"status":"success","data":{"attributes":[],"totalKeys":0}}`), nil
		},
	}

	h := newTestHandler(mock)
	req := makeToolRequest("signoz_check_metric_cardinality", map[string]any{
		"metricName": "system.cpu.time",
		// no timeRange, start, or end — should default to 7d
	})

	result, err := h.handleCheckMetricCardinality(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	sevenDaysMs := int64(7 * 24 * 60 * 60 * 1000)
	diff := capturedEnd - capturedStart
	// Allow ±5s for test execution time
	tolerance := int64(5000)
	if diff < sevenDaysMs-tolerance || diff > sevenDaysMs+tolerance {
		t.Errorf("expected ~7d window (%d ms), got %d ms (start=%d end=%d)", sevenDaysMs, diff, capturedStart, capturedEnd)
	}
}

func TestHandleCheckMetricCardinality_ExplicitStartEnd(t *testing.T) {
	var capturedStart, capturedEnd int64
	mock := &client.MockClient{
		GetMetricCardinalityFn: func(_ context.Context, _ string, start, end int64) (json.RawMessage, error) {
			capturedStart = start
			capturedEnd = end
			return json.RawMessage(`{"status":"success","data":{"attributes":[],"totalKeys":0}}`), nil
		},
	}

	h := newTestHandler(mock)
	req := makeToolRequest("signoz_check_metric_cardinality", map[string]any{
		"metricName": "system.cpu.time",
		"start":      float64(1781007164240),
		"end":        float64(1781611964240),
	})

	result, err := h.handleCheckMetricCardinality(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	if capturedStart != 1781007164240 {
		t.Errorf("expected start 1781007164240, got %d", capturedStart)
	}
	if capturedEnd != 1781611964240 {
		t.Errorf("expected end 1781611964240, got %d", capturedEnd)
	}
}
