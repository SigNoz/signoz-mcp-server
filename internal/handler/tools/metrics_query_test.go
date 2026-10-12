package tools

import (
	"encoding/json"
	"testing"
)

// TestParseMetricMetadataFromResponse_PartialFieldDrift (FIX D3) pins that when a
// metric row matches by name and carries a type but is MISSING its companion
// fields (temporality / isMonotonic), the decoded result is DETECTABLY
// incomplete (drift flags set, empty temporality) instead of silently decoding
// a wrong non-monotonic default as authoritative. The WARN itself fires in
// fetchMetricMetadata; here we assert the parse result the WARN keys off.
func TestParseMetricMetadataFromResponse_PartialFieldDrift(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		wantType        string
		wantTemporality string
		wantTempMissing bool
		wantMonotonic   bool
		wantMonoMissing bool
	}{
		{
			name:            "sum missing temporality and isMonotonic (drift)",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"http.server.duration","type":"sum"}]}}`,
			wantType:        "sum",
			wantTemporality: "",
			wantTempMissing: true,
			wantMonotonic:   false,
			wantMonoMissing: true,
		},
		{
			name:            "sum with full metadata (no drift)",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"http.server.duration","type":"sum","temporality":"Cumulative","isMonotonic":true}]}}`,
			wantType:        "sum",
			wantTemporality: "Cumulative",
			wantTempMissing: false,
			wantMonotonic:   true,
			wantMonoMissing: false,
		},
		{
			name:            "sum explicit isMonotonic false is NOT drift",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"http.server.duration","type":"sum","temporality":"Delta","isMonotonic":false}]}}`,
			wantType:        "sum",
			wantTemporality: "Delta",
			wantTempMissing: false,
			wantMonotonic:   false,
			wantMonoMissing: false,
		},
		{
			name:            "explicit empty temporality is present, NOT drift",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"system.cpu.utilization","type":"gauge","temporality":""}]}}`,
			wantType:        "gauge",
			wantTemporality: "",
			wantTempMissing: false,
			wantMonotonic:   false,
			wantMonoMissing: false,
		},
		{
			name:            "gauge omitting isMonotonic is NOT drift (only temporality flagged)",
			body:            `{"status":"success","data":{"metrics":[{"metricName":"system.cpu.utilization","type":"gauge"}]}}`,
			wantType:        "gauge",
			wantTemporality: "",
			wantTempMissing: true,
			wantMonotonic:   false,
			wantMonoMissing: false,
		},
		{
			name:            "array format, sum drift",
			body:            `[{"metricName":"http.server.duration","type":"sum"}]`,
			wantType:        "sum",
			wantTemporality: "",
			wantTempMissing: true,
			wantMonotonic:   false,
			wantMonoMissing: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta, err := parseMetricMetadataFromResponse(json.RawMessage(tc.body), "http.server.duration")
			if tc.wantType == "gauge" {
				meta, err = parseMetricMetadataFromResponse(json.RawMessage(tc.body), "system.cpu.utilization")
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta == nil {
				t.Fatalf("expected a matched metric, got nil")
			}
			if meta.MetricType != tc.wantType {
				t.Fatalf("MetricType = %q, want %q", meta.MetricType, tc.wantType)
			}
			if meta.Temporality != tc.wantTemporality {
				t.Fatalf("Temporality = %q, want %q", meta.Temporality, tc.wantTemporality)
			}
			if meta.TemporalityMissing != tc.wantTempMissing {
				t.Fatalf("TemporalityMissing = %v, want %v", meta.TemporalityMissing, tc.wantTempMissing)
			}
			if meta.IsMonotonic != tc.wantMonotonic {
				t.Fatalf("IsMonotonic = %v, want %v", meta.IsMonotonic, tc.wantMonotonic)
			}
			if meta.IsMonotonicMissing != tc.wantMonoMissing {
				t.Fatalf("IsMonotonicMissing = %v, want %v", meta.IsMonotonicMissing, tc.wantMonoMissing)
			}
		})
	}
}
