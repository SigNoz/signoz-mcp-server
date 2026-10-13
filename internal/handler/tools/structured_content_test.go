package tools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Two-tier structuredContent rule:
//   - Code-controlled tools (paginate.Wrap list/summary, single-resource get_*,
//     and synthesized-JSON mutations) carry structuredContent.
//   - Raw QB passthrough tools (search/aggregate/query_metrics) do NOT, because
//     their upstream JSON shape is variable and an outputSchema would be brittle.
//
// These tests pin both halves of that rule so a future change can't silently
// add a brittle outputSchema to a passthrough tool or drop it from a
// code-controlled one.

// --- structuredResult helper unit test ---

func TestStructuredResult_CarriesSameJSONInBothRepresentations(t *testing.T) {
	payload := []byte(`{"pagination":{"total":2},"data":[{"id":"a"},{"id":"b"}]}`)
	res := structuredResult(payload)

	if res.IsError {
		t.Fatalf("structuredResult should not be an error result")
	}
	if res.StructuredContent == nil {
		t.Fatalf("structuredResult must populate StructuredContent")
	}
	// Block 0 carries the exact JSON string for back-compat clients.
	if got := textContent(t, res); got != string(payload) {
		t.Fatalf("text block = %q, want %q", got, string(payload))
	}
	// StructuredContent must decode to the same value as the original payload.
	// Compare with json.Number on BOTH sides so a precision-losing float64
	// decode (the bug Codex caught) would surface as a mismatch rather than be
	// masked by both sides rounding identically. Re-decode (not raw bytes) so
	// map key ordering doesn't make an equal value look different.
	if !sameJSONValue(t, payload, res.StructuredContent) {
		gotBytes, _ := json.Marshal(res.StructuredContent)
		t.Fatalf("structured content does not match payload\npayload=%s\nstructured=%s", payload, gotBytes)
	}
}

// sameJSONValue reports whether the StructuredContent value re-marshals to the
// same JSON *value* as wantJSON, comparing with json.Number so large integers
// (>2^53) are compared by exact literal rather than lossy float64.
func sameJSONValue(t *testing.T, wantJSON []byte, structured any) bool {
	t.Helper()
	decodeNum := func(b []byte) any {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return v
	}
	gotBytes, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return reflect.DeepEqual(decodeNum(wantJSON), decodeNum(gotBytes))
}

// TestStructuredResult_PreservesLargeIntegers pins the fix for the precision
// blocker: SigNoz integers above 2^53 (epoch nanos, big ids/counts) must appear
// byte-for-byte identically in StructuredContent and the text block. A naive
// json.Unmarshal into `any` would route them through float64 and round them.
func TestStructuredResult_PreservesLargeIntegers(t *testing.T) {
	// 9007199254740993 = 2^53 + 1, the smallest integer float64 cannot represent
	// exactly (it rounds to 9007199254740992).
	const bigInt = "9007199254740993"
	payload := []byte(`{"id":"x","timestampNano":` + bigInt + `,"count":` + bigInt + `}`)
	res := structuredResult(payload)

	if res.StructuredContent == nil {
		t.Fatalf("structuredResult must populate StructuredContent")
	}
	// Text block must be byte-faithful.
	if got := textContent(t, res); got != string(payload) {
		t.Fatalf("text block = %q, want %q", got, string(payload))
	}
	// StructuredContent must re-marshal with the EXACT large literal, not a
	// rounded value (no 9007199254740992, no 9.007199254740993e+15).
	gotBytes, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	got := string(gotBytes)
	if !strings.Contains(got, bigInt) {
		t.Fatalf("StructuredContent lost integer precision: %s (want literal %s)", got, bigInt)
	}
	if strings.Contains(got, "9007199254740992") || strings.Contains(got, "e+") || strings.Contains(got, "E+") {
		t.Fatalf("StructuredContent rounded/exponential-formatted the big integer: %s", got)
	}
}

// TestStructuredResult_PreservesNestedLargeIntegers extends the precision
// guard to nested objects and array elements — real SigNoz payloads carry big
// integers (epoch nanos, counts) inside rows[] and meta, not just at the top
// level.
func TestStructuredResult_PreservesNestedLargeIntegers(t *testing.T) {
	const bigInt = "9007199254740993" // 2^53 + 1, the smallest float64-inexact int
	payload := []byte(`{"rows":[{"ts":` + bigInt + `}],"meta":{"count":` + bigInt + `}}`)
	res := structuredResult(payload)

	if res.StructuredContent == nil {
		t.Fatalf("structuredResult must populate StructuredContent")
	}
	if got := textContent(t, res); got != string(payload) {
		t.Fatalf("text block = %q, want %q", got, string(payload))
	}
	gotBytes, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	got := string(gotBytes)
	// Both the array-element ts and the nested meta.count must keep the exact
	// literal — count the occurrences so a single surviving copy can't mask a
	// rounded sibling.
	if n := strings.Count(got, bigInt); n != 2 {
		t.Fatalf("nested large ints not preserved: found %d of literal %s in %s", n, bigInt, got)
	}
	if strings.Contains(got, "9007199254740992") || strings.Contains(got, "e+") || strings.Contains(got, "E+") {
		t.Fatalf("StructuredContent rounded/exponential-formatted a nested big integer: %s", got)
	}
}

func TestStructuredResult_FailsOpenOnInvalidJSON(t *testing.T) {
	// Should never happen for a code-controlled tool, but must not error out.
	res := structuredResult([]byte(`not json`))
	if res.IsError {
		t.Fatalf("structuredResult should fail open, not error")
	}
	if res.StructuredContent != nil {
		t.Fatalf("invalid JSON should not populate StructuredContent, got %#v", res.StructuredContent)
	}
	if got := textContent(t, res); got != "not json" {
		t.Fatalf("text block = %q, want raw payload", got)
	}
}

// TestStructuredResult_FailsOpenOnTrailingData guards that a payload with a
// valid first value but trailing junk / a second value does NOT advertise a
// StructuredContent that only covers the first value (which would silently
// disagree with the whole-payload text block 0). It must fail open to text-only.
func TestStructuredResult_FailsOpenOnTrailingData(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"trailing junk", `{"id":"a"} garbage`},
		{"two values", `{"id":"a"}{"id":"b"}`},
		{"value then array", `{"id":"a"} [1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := structuredResult([]byte(tc.payload))
			if res.IsError {
				t.Fatalf("should fail open, not error")
			}
			if res.StructuredContent != nil {
				t.Fatalf("trailing data must NOT populate StructuredContent, got %#v", res.StructuredContent)
			}
			// Text block still carries the whole raw payload.
			if got := textContent(t, res); got != tc.payload {
				t.Fatalf("text block = %q, want raw payload %q", got, tc.payload)
			}
		})
	}

	// A single value with only trailing whitespace is still valid and MUST keep
	// structuredContent (the EOF check must not reject benign whitespace).
	res := structuredResult([]byte("{\"id\":\"a\"}\n  \t"))
	if res.StructuredContent == nil {
		t.Fatalf("single value with trailing whitespace must keep StructuredContent")
	}
}

// --- structuredContent PRESENT on code-controlled tools ---

// --- structuredContent ABSENT on raw QB passthrough tools ---
