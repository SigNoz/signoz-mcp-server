package tools

import (
	"strings"
	"testing"
)

// --- shared boolean parser ---

func TestParseBoolArg(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		wantVal   bool
		wantOK    bool
		wantError bool
	}{
		{name: "real bool true", args: map[string]any{"k": true}, wantVal: true, wantOK: true},
		{name: "real bool false", args: map[string]any{"k": false}, wantVal: false, wantOK: true},
		{name: "string true", args: map[string]any{"k": "true"}, wantVal: true, wantOK: true},
		{name: "string false", args: map[string]any{"k": "false"}, wantVal: false, wantOK: true},
		{name: "string TRUE case-insensitive", args: map[string]any{"k": "TRUE"}, wantVal: true, wantOK: true},
		{name: "string False case-insensitive", args: map[string]any{"k": "False"}, wantVal: false, wantOK: true},
		{name: "string padded true", args: map[string]any{"k": "  true  "}, wantVal: true, wantOK: true},
		{name: "absent", args: map[string]any{}, wantVal: false, wantOK: false},
		{name: "nil value", args: map[string]any{"k": nil}, wantVal: false, wantOK: false},
		{name: "empty string treated as absent", args: map[string]any{"k": ""}, wantVal: false, wantOK: false},
		{name: "garbage string errors", args: map[string]any{"k": "maybe"}, wantError: true},
		{name: "numeric string errors", args: map[string]any{"k": "1"}, wantError: true},
		{name: "number type errors", args: map[string]any{"k": float64(1)}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok, err := parseBoolArg(tt.args, "k")
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got val=%v ok=%v", val, ok)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tt.wantVal || ok != tt.wantOK {
				t.Fatalf("got (val=%v ok=%v), want (val=%v ok=%v)", val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

// The `error` trace filter must hard-error on garbage rather than silently
// dropping the filter (which widened results).
func TestParseSearchTracesArgs_ErrorFilter(t *testing.T) {
	// real bool true -> has_error = true
	reqTrue, err := parseSearchTracesArgs(map[string]any{"error": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reqTrue.FilterExpression, "has_error = true") {
		t.Fatalf("expected has_error = true in filter, got %q", reqTrue.FilterExpression)
	}

	// string "false" -> has_error = false
	reqFalse, err := parseSearchTracesArgs(map[string]any{"error": "false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reqFalse.FilterExpression, "has_error = false") {
		t.Fatalf("expected has_error = false in filter, got %q", reqFalse.FilterExpression)
	}

	// absent -> no generated error clause
	reqAbsent, err := parseSearchTracesArgs(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(reqAbsent.FilterExpression, "has_error") {
		t.Fatalf("expected no has_error clause when absent, got %q", reqAbsent.FilterExpression)
	}

	// garbage -> hard error (previously silently dropped + widened results)
	if _, err := parseSearchTracesArgs(map[string]any{"error": "yes"}); err == nil {
		t.Fatal("expected hard error on garbage error value, got nil")
	}
}

// query_metrics hard-errors on a garbage isMonotonic and honors a valid one.
func TestParseMetricsQueryArgs_IsMonotonicGarbage(t *testing.T) {
	if _, err := parseMetricsQueryArgs(map[string]any{"metricName": "m", "isMonotonic": "yes"}); err == nil {
		t.Fatal("expected error on garbage isMonotonic, got nil")
	}
	req, err := parseMetricsQueryArgs(map[string]any{"metricName": "m", "isMonotonic": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !req.IsMonotonic {
		t.Fatal("expected IsMonotonic=true")
	}
}

// A suffixed string like "1h"/"60s" must NOT be accepted as a numeric prefix
// (1/60); it must warn + auto-select instead of applying a silently-wrong
// bucket size. Direct parseStepInterval coverage + via the aggregate parser.
func TestParseStepInterval_RejectsSuffixedStrings(t *testing.T) {
	tests := []struct {
		name     string
		in       any
		wantNil  bool
		wantWarn bool
		wantVal  int64
	}{
		{name: "whole-int string", in: "60", wantVal: 60},
		{name: "json number", in: float64(120), wantVal: 120},
		{name: "duration 1h rejected", in: "1h", wantNil: true, wantWarn: true},
		{name: "duration 60s rejected", in: "60s", wantNil: true, wantWarn: true},
		{name: "garbage abc rejected", in: "abc", wantNil: true, wantWarn: true},
		{name: "hex-ish rejected", in: "0x10", wantNil: true, wantWarn: true},
		{name: "negative rejected", in: "-5", wantNil: true, wantWarn: true},
		{name: "zero rejected", in: "0", wantNil: true, wantWarn: true},
		{name: "empty string -> not set, no warn", in: "", wantNil: true, wantWarn: false},
		{name: "absent (nil) -> not set, no warn", in: nil, wantNil: true, wantWarn: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			si, warn := parseStepInterval(tt.in)
			if tt.wantNil && si != nil {
				t.Fatalf("expected nil interval, got %d", *si)
			}
			if !tt.wantNil {
				if si == nil {
					t.Fatalf("expected interval %d, got nil", tt.wantVal)
				}
				if *si != tt.wantVal {
					t.Fatalf("interval = %d, want %d", *si, tt.wantVal)
				}
			}
			if tt.wantWarn && warn == "" {
				t.Fatalf("expected a warning for %v", tt.in)
			}
			if !tt.wantWarn && warn != "" {
				t.Fatalf("unexpected warning for %v: %q", tt.in, warn)
			}
		})
	}

	// End-to-end through the aggregate parser: an accepted value reaches the
	// request, and "1h" must not yield 1.
	accepted, err := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": float64(60),
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accepted.StepInterval == nil || *accepted.StepInterval != 60 {
		t.Fatalf("expected stepInterval=60 to reach the aggregate request, got %v", accepted.StepInterval)
	}
	req, err := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.StepInterval != nil {
		t.Fatalf(`"1h" must be rejected, not parsed as %d`, *req.StepInterval)
	}
	if req.StepIntervalWarning == "" {
		t.Fatal(`expected a warning for "1h"`)
	}
}

// --- completeness notes ---

func TestCountQueryRangeRows(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantN   int
		wantOK  bool
	}{
		{
			name:    "two results with rows",
			payload: `{"data":{"data":{"results":[{"rows":[{"data":{}},{"data":{}}]},{"rows":[{"data":{}}]}]}}}`,
			wantN:   3, wantOK: true,
		},
		{
			name:    "genuinely empty results array is known-zero",
			payload: `{"data":{"data":{"results":[]}}}`,
			wantN:   0, wantOK: true,
		},
		{
			name:    "result present with empty rows array is known-zero",
			payload: `{"data":{"data":{"results":[{"rows":[]}]}}}`,
			wantN:   0, wantOK: true,
		},
		// --- MISSING key / non-array-non-null leaf -> uncountable (fail open) ---
		{
			name:    "result object missing rows key -> fail open",
			payload: `{"data":{"data":{"results":[{}]}}}`,
			wantOK:  false,
		},
		{
			name:    "results key missing -> fail open",
			payload: `{"data":{"data":{}}}`,
			wantOK:  false,
		},
		{
			name:    "results not an array -> fail open",
			payload: `{"data":{"data":{"results":{}}}}`,
			wantOK:  false,
		},
		{
			name:    "rows not an array -> fail open",
			payload: `{"data":{"data":{"results":[{"rows":{}}]}}}`,
			wantOK:  false,
		},
		{
			name:    "non-QB shape -> fail open",
			payload: `{"data":[]}`,
			wantOK:  false,
		},
		// --- present-null leaf -> NORMAL empty (known-zero), matching the
		// InjectRowsWebURL contract. NOT fail-open. ---
		{
			name:    "present null results -> known-zero",
			payload: `{"data":{"data":{"results":null}}}`,
			wantN:   0, wantOK: true,
		},
		{
			name:    "result object with present null rows -> known-zero",
			payload: `{"data":{"data":{"results":[{"rows":null}]}}}`,
			wantN:   0, wantOK: true,
		},
		{
			name:    "mix of present-null rows and a populated result",
			payload: `{"data":{"data":{"results":[{"rows":null},{"rows":[{"data":{}},{"data":{}}]}]}}}`,
			wantN:   2, wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := countQueryRangeRows([]byte(tt.payload))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (n=%d)", ok, tt.wantOK, n)
			}
			if tt.wantOK && n != tt.wantN {
				t.Fatalf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestCountDataArrayRows(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		key     string
		wantN   int
		wantOK  bool
	}{
		{name: "two items", payload: `{"data":{"items":[{},{}]}}`, key: "items", wantN: 2, wantOK: true},
		{name: "empty array is known-zero", payload: `{"data":{"metrics":[]}}`, key: "metrics", wantN: 0, wantOK: true},
		// present-null leaf -> NORMAL empty (known-zero), not fail-open.
		{name: "present null key -> known-zero", payload: `{"data":{"metrics":null}}`, key: "metrics", wantN: 0, wantOK: true},
		// --- MISSING key / non-array-non-null -> fail open ---
		{name: "key absent -> fail open", payload: `{"data":{}}`, key: "items", wantOK: false},
		{name: "key not an array -> fail open", payload: `{"data":{"samples":{}}}`, key: "samples", wantOK: false},
		{name: "data missing -> fail open", payload: `{"status":"success"}`, key: "items", wantOK: false},
		{name: "data null -> fail open", payload: `{"data":null}`, key: "items", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := countDataArrayRows([]byte(tt.payload), tt.key)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (n=%d)", ok, tt.wantOK, n)
			}
			if tt.wantOK && n != tt.wantN {
				t.Fatalf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestCountAlertHistoryRowsFamilyADataShapes(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantN   int
		wantOK  bool
	}{
		{name: "top-level data array counts", payload: `{"status":"success","data":[{},{}]}`, wantN: 2, wantOK: true},
		{name: "data items array counts", payload: `{"status":"success","data":{"items":[{}, {}, {}]}}`, wantN: 3, wantOK: true},
		{name: "present data null is known zero", payload: `{"status":"success","data":null}`, wantN: 0, wantOK: true},
		{name: "present items null is known zero", payload: `{"status":"success","data":{"items":null}}`, wantN: 0, wantOK: true},
		{name: "missing data fails open", payload: `{"status":"success"}`, wantOK: false},
		{name: "data object without items fails open", payload: `{"status":"success","data":{}}`, wantOK: false},
		{name: "data string fails open", payload: `{"status":"success","data":"unexpected"}`, wantOK: false},
		{name: "items object fails open", payload: `{"status":"success","data":{"items":{}}}`, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := countAlertHistoryRows([]byte(tt.payload))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (n=%d)", ok, tt.wantOK, n)
			}
			if tt.wantOK && n != tt.wantN {
				t.Fatalf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestCompletenessNote_HasMore(t *testing.T) {
	// returnedRows == limit -> hasMore=true, nextOffset advances
	note := completenessNote(100, 100, 0, true)
	if !strings.Contains(note, "hasMore=true") || !strings.Contains(note, "offset=100") {
		t.Fatalf("expected hasMore=true + offset=100, got %q", note)
	}
	// returnedRows < limit -> hasMore=false
	note = completenessNote(7, 100, 0, true)
	if !strings.Contains(note, "hasMore=false") {
		t.Fatalf("expected hasMore=false, got %q", note)
	}
	// unknown row count -> generic note
	note = completenessNote(0, 100, 0, false)
	if !strings.Contains(note, "limit 100 applied") {
		t.Fatalf("expected generic note when row count unknown, got %q", note)
	}
}
