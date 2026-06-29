package util

import (
	"strings"
	"testing"
)

func TestInjectWebURL_PreservesLargeInt64(t *testing.T) {
	// A duration_nano-style int64 that exceeds float64's exact-integer range
	// (2^53). A naive Unmarshal->map[string]any->Marshal round-trip would coerce
	// it through float64 and round it; the shallow RawMessage decode must pass
	// it through as verbatim bytes.
	const bigInt = "9007199254740993" // 2^53 + 1, not exactly representable as float64
	in := []byte(`{"data":{"duration_nano":` + bigInt + `,"name":"GET /cart"}}`)

	out := InjectWebURL(in, "https://signoz.example.com", "trace", "abc-123")
	s := string(out)

	if !strings.Contains(s, bigInt) {
		t.Fatalf("large int64 lost precision: %s", s)
	}
	if strings.Contains(s, "9.007") || strings.Contains(s, "e+") || strings.Contains(s, "E+") {
		t.Fatalf("int64 was coerced to float: %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("webUrl not injected on inner data object: %s", s)
	}
}

func TestInjectWebURL_BareBody(t *testing.T) {
	in := []byte(`{"uuid":"x","spans":[]}`)
	out := InjectWebURL(in, "https://signoz.example.com", "dashboard", "x")
	if !strings.Contains(string(out), `"webUrl":"https://signoz.example.com/dashboard/x"`) {
		t.Fatalf("webUrl not injected at top level: %s", out)
	}
}

func TestInjectWebURL_PreservesInnerBytesVerbatim(t *testing.T) {
	// Values nested below the injection level must pass through as verbatim
	// bytes: a full-tree decode/re-marshal would sort zKey/aKey alphabetically.
	in := []byte(`{"data":{"spans":[{"zKey":1,"aKey":2.50}],"duration_nano":9007199254740993}}`)
	out := InjectWebURL(in, "https://signoz.example.com", "trace", "abc-123")
	s := string(out)

	if !strings.Contains(s, `[{"zKey":1,"aKey":2.50}]`) {
		t.Fatalf("inner bytes were re-encoded (key order or number formatting changed): %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("webUrl not injected on inner data object: %s", s)
	}
}

func TestInjectWebURL_NullBodyReturnsOriginal(t *testing.T) {
	in := []byte(`null`)
	out := InjectWebURL(in, "https://signoz.example.com", "trace", "abc-123")
	if string(out) != string(in) {
		t.Fatalf("expected original bytes for null body, got: %s", out)
	}
}

func TestInjectWebURL_NullDataInjectsTopLevel(t *testing.T) {
	in := []byte(`{"data":null}`)
	out := InjectWebURL(in, "https://signoz.example.com", "trace", "abc-123")
	s := string(out)
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("webUrl not injected at top level when data is null: %s", s)
	}
	if !strings.Contains(s, `"data":null`) {
		t.Fatalf("null data value not preserved: %s", s)
	}
}

func TestInjectWebURL_ArrayDataInjectsTopLevel(t *testing.T) {
	in := []byte(`{"data":[1,2]}`)
	out := InjectWebURL(in, "https://signoz.example.com", "trace", "abc-123")
	s := string(out)
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("webUrl not injected at top level when data is an array: %s", s)
	}
	if !strings.Contains(s, `"data":[1,2]`) {
		t.Fatalf("array data value not preserved: %s", s)
	}
}

func TestInjectWebURL_NoBaseReturnsOriginal(t *testing.T) {
	in := []byte(`{"data":{"duration_nano":9007199254740993}}`)
	out := InjectWebURL(in, "", "trace", "abc-123")
	if string(out) != string(in) {
		t.Fatalf("expected original bytes when base empty, got: %s", out)
	}
}

func TestInjectWebURL_UnknownTypeReturnsOriginal(t *testing.T) {
	in := []byte(`{"data":{"x":1}}`)
	out := InjectWebURL(in, "https://signoz.example.com", "log", "id")
	if string(out) != string(in) {
		t.Fatalf("expected original bytes for unknown type, got: %s", out)
	}
}

func TestInjectWebURL_MalformedReturnsOriginal(t *testing.T) {
	in := []byte(`not json`)
	out := InjectWebURL(in, "https://signoz.example.com", "trace", "abc-123")
	if string(out) != string(in) {
		t.Fatalf("expected original bytes for malformed body, got: %s", out)
	}
}

// rawTracesBody returns a realistic query-builder v5 "raw" passthrough body — a
// render.Success envelope wrapping QueryRangeResponse — with two rows. The
// second row's duration_nano exceeds float64's exact-integer range to guard the
// precision bug. The selected trace-id field is "trace_id", matching the column
// alias the backend emits for the canonical trace_id select field.
func rawTracesBody() []byte {
	return []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[` +
		`{"timestamp":"2026-06-19T10:00:00Z","data":{"trace_id":"abc-123","duration_nano":9007199254740993,"name":"GET /cart"}},` +
		`{"timestamp":"2026-06-19T10:00:01Z","data":{"trace_id":"def-456","duration_nano":42,"name":"POST /checkout"}}` +
		`]}]},"meta":{}}}`)
}

func TestInjectRowsWebURL_InjectsPerRow(t *testing.T) {
	out, _ := InjectRowsWebURL(rawTracesBody(), "https://signoz.example.com", "trace", "trace_id")
	s := string(out)

	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("first row webUrl missing: %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/def-456"`) {
		t.Fatalf("second row webUrl missing: %s", s)
	}
}

func TestInjectRowsWebURL_UsesTraceIDFallbackKeys(t *testing.T) {
	in := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[` +
		`{"timestamp":"t","data":{"trace_id":"canonical"}},` +
		`{"timestamp":"t","data":{"traceID":"legacy-caps"}},` +
		`{"timestamp":"t","data":{"traceId":"legacy-camel"}}` +
		`]}]},"meta":{}}}`)
	out, res := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id", "traceID", "traceId")
	s := string(out)

	if res.RowsSeen != 3 || res.RowsEnriched != 3 {
		t.Fatalf("expected all mixed trace-id rows enriched, got seen=%d enriched=%d body=%s", res.RowsSeen, res.RowsEnriched, s)
	}
	for _, id := range []string{"canonical", "legacy-caps", "legacy-camel"} {
		if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/`+id+`"`) {
			t.Fatalf("expected webUrl for %s, got: %s", id, s)
		}
	}
}

func TestInjectRowsWebURL_PreservesLargeInt64(t *testing.T) {
	// 2^53 + 1: a duration_nano-style int64 that loses precision if coerced
	// through float64. The shallow RawMessage decode must pass it through verbatim.
	const bigInt = "9007199254740993"
	out, _ := InjectRowsWebURL(rawTracesBody(), "https://signoz.example.com", "trace", "trace_id")
	s := string(out)

	if !strings.Contains(s, bigInt) {
		t.Fatalf("large int64 lost precision: %s", s)
	}
	if strings.Contains(s, "9.007") || strings.Contains(s, "e+") || strings.Contains(s, "E+") {
		t.Fatalf("int64 was coerced to float: %s", s)
	}
}

func TestInjectRowsWebURL_NoBaseReturnsOriginal(t *testing.T) {
	in := rawTracesBody()
	out, _ := InjectRowsWebURL(in, "", "trace", "trace_id")
	if string(out) != string(in) {
		t.Fatalf("expected original bytes when base empty, got: %s", out)
	}
}

func TestInjectRowsWebURL_MissingOrEmptyIDLeftUntouched(t *testing.T) {
	// One row has no trace_id, one has an empty trace_id, one is well-formed.
	in := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[` +
		`{"timestamp":"t","data":{"name":"no-id"}},` +
		`{"timestamp":"t","data":{"trace_id":"","name":"empty-id"}},` +
		`{"timestamp":"t","data":{"trace_id":"ok-789","name":"good"}}` +
		`]}]},"meta":{}}}`)
	out, _ := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
	s := string(out)

	if strings.Count(s, `"webUrl"`) != 1 {
		t.Fatalf("expected exactly one webUrl (only the well-formed row), got: %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/ok-789"`) {
		t.Fatalf("well-formed row webUrl missing: %s", s)
	}
	if strings.Contains(s, `/trace/"`) {
		t.Fatalf("emitted a broken empty-id webUrl: %s", s)
	}
}

func TestInjectRowsWebURL_PreservesSiblingBytesVerbatim(t *testing.T) {
	// Fields outside the mutated row "data" object — and sibling rows — must pass
	// through verbatim; a full-tree re-marshal would reorder keys or reformat
	// numbers. nextCursor and the meta block sit alongside the mutated results.
	in := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","nextCursor":"zKey","rows":[` +
		`{"timestamp":"t","data":{"traceID":"abc-123","duration_nano":9007199254740993}}` +
		`]}]},"meta":{"rowsScanned":2.50}}}`)
	out, _ := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id", "traceID")
	s := string(out)

	if !strings.Contains(s, `"nextCursor":"zKey"`) {
		t.Fatalf("sibling nextCursor not preserved verbatim: %s", s)
	}
	if !strings.Contains(s, `"meta":{"rowsScanned":2.50}`) {
		t.Fatalf("sibling meta not preserved verbatim (number reformatted?): %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("row webUrl missing: %s", s)
	}
}

func TestInjectRowsWebURL_MalformedReturnsOriginal(t *testing.T) {
	cases := map[string][]byte{
		"not json":            []byte(`not json`),
		"null body":           []byte(`null`),
		"array body":          []byte(`[1,2,3]`),
		"no data envelope":    []byte(`{"status":"success"}`),
		"no inner data":       []byte(`{"status":"success","data":{"type":"raw"}}`),
		"results not array":   []byte(`{"status":"success","data":{"type":"raw","data":{"results":{}}}}`),
		"rows missing":        []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A"}]}}}`),
		"empty results array": []byte(`{"status":"success","data":{"type":"raw","data":{"results":[]}}}`),
		"row not object":      []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"rows":[1,2]}]}}}`),
		"row data not object": []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"rows":[{"data":5}]}]}}}`),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, _ := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
			if string(out) != string(in) {
				t.Fatalf("expected original bytes unchanged, got: %s", out)
			}
		})
	}
}

func TestInjectRowsWebURL_OverwritesExistingWebURL(t *testing.T) {
	// A row that already carries a (stale) webUrl must end up with the freshly
	// built one and exactly one webUrl key — never a duplicate.
	in := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"rows":[` +
		`{"data":{"trace_id":"abc-123","webUrl":"https://stale.example.com/trace/old"}}` +
		`]}]},"meta":{}}}`)
	out, _ := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
	s := string(out)

	if strings.Count(s, `"webUrl"`) != 1 {
		t.Fatalf("expected exactly one webUrl after overwrite, got: %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/abc-123"`) {
		t.Fatalf("stale webUrl not overwritten: %s", s)
	}
	if strings.Contains(s, "stale.example.com") {
		t.Fatalf("stale webUrl still present: %s", s)
	}
}

func TestInjectRowsWebURL_EscapesTraceID(t *testing.T) {
	// Trace ids flow through url.PathEscape in ResourceWebURL; a value with
	// reserved characters must be percent-encoded in the row's webUrl.
	in := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"rows":[` +
		`{"data":{"trace_id":"a/b c"}}` +
		`]}]},"meta":{}}}`)
	out, _ := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
	s := string(out)

	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/a%2Fb%20c"`) {
		t.Fatalf("trace id not escaped in webUrl: %s", s)
	}
}

func TestInjectRowsWebURL_MultipleResultsMixed(t *testing.T) {
	// Enrichment must span every result in the array (not just the first) and
	// skip malformed rows within each result independently.
	in := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[` +
		`{"queryName":"A","rows":[{"data":{"trace_id":"a-1"}},{"data":{"name":"no-id"}}]},` +
		`{"queryName":"B","rows":[{"data":{"trace_id":"b-1"}}]}` +
		`]},"meta":{}}}`)
	out, _ := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
	s := string(out)

	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/a-1"`) {
		t.Fatalf("first result row not linked: %s", s)
	}
	if !strings.Contains(s, `"webUrl":"https://signoz.example.com/trace/b-1"`) {
		t.Fatalf("second result (queryName B) row not linked: %s", s)
	}
	if strings.Count(s, `"webUrl"`) != 2 {
		t.Fatalf("expected exactly two webUrls (one per good row across results), got: %s", s)
	}
}

func TestInjectRowsWebURL_ReportsCounts(t *testing.T) {
	// Happy path: results reached, two rows seen, two enriched.
	_, res := InjectRowsWebURL(rawTracesBody(), "https://signoz.example.com", "trace", "trace_id")
	if !res.ResultsReached || res.RowsSeen != 2 || res.RowsEnriched != 2 {
		t.Fatalf("happy path: got ResultsReached=%v RowsSeen=%d RowsEnriched=%d, want true/2/2",
			res.ResultsReached, res.RowsSeen, res.RowsEnriched)
	}
}

func TestInjectRowsWebURL_ReportsColumnAliasDriftVsNoData(t *testing.T) {
	// Column-alias drift: results reached and rows ARE present, but none carry
	// the expected id key, so RowsSeen > 0 while RowsEnriched == 0. The handler
	// turns this into a WARN. The body is still returned verbatim.
	drift := rawTracesBody()
	out, res := InjectRowsWebURL(drift, "https://signoz.example.com", "trace", "traceID") // wrong key
	if !res.ResultsReached || res.RowsSeen == 0 || res.RowsEnriched != 0 {
		t.Fatalf("drift: got ResultsReached=%v RowsSeen=%d RowsEnriched=%d, want true/>0/0",
			res.ResultsReached, res.RowsSeen, res.RowsEnriched)
	}
	if string(out) != string(drift) {
		t.Fatalf("drift must return original bytes unchanged, got: %s", out)
	}

	// Ordinary "no data": results reached but zero rows — the handler stays
	// silent (no false drift warning).
	noData := []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[]}]}},"meta":{}}`)
	_, res = InjectRowsWebURL(noData, "https://signoz.example.com", "trace", "trace_id")
	if !res.ResultsReached || res.RowsSeen != 0 || res.RowsEnriched != 0 {
		t.Fatalf("no-data: got ResultsReached=%v RowsSeen=%d RowsEnriched=%d, want true/0/0",
			res.ResultsReached, res.RowsSeen, res.RowsEnriched)
	}
}

func TestInjectRowsWebURL_FlagsUnwalkableEnvelope(t *testing.T) {
	// Envelope drift: the response carries data, but results[] is renamed/moved
	// so the expected nesting can't be walked. ResultsReached must be false
	// (distinct from an empty result), and the body is returned verbatim.
	cases := map[string][]byte{
		"results renamed":    []byte(`{"status":"success","data":{"type":"raw","data":{"rezults":[{"rows":[{"data":{"trace_id":"x"}}]}]}},"meta":{}}`),
		"inner data renamed": []byte(`{"status":"success","data":{"type":"raw","payload":{"results":[{"rows":[{"data":{"trace_id":"x"}}]}]}},"meta":{}}`),
		"results not array":  []byte(`{"status":"success","data":{"type":"raw","data":{"results":{"rows":[]}}}}`),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			out, res := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
			if res.ResultsReached {
				t.Fatalf("expected ResultsReached=false for unwalkable envelope, got true")
			}
			if string(out) != string(in) {
				t.Fatalf("expected original bytes unchanged, got: %s", out)
			}
		})
	}
}

func TestInjectRowsWebURL_FlagsRowsKeyDrift(t *testing.T) {
	// results[] IS reachable and non-empty, but the per-result "rows" key is
	// renamed/removed/wrong-type so no rows array can be read. This must read as
	// drift (ResultCount > 0, RowsArraysReached == 0), distinct from an empty
	// result. The body is returned verbatim.
	drift := map[string][]byte{
		"rows renamed": []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","records":[{"data":{"trace_id":"x"}}]}]}},"meta":{}}`),
		"rows absent":  []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A"}]}},"meta":{}}`),
		"rows object":  []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":{"0":{"data":{"trace_id":"x"}}}}]}},"meta":{}}`),
	}
	for name, in := range drift {
		t.Run(name, func(t *testing.T) {
			out, res := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
			if !res.ResultsReached || res.ResultCount == 0 || res.RowsArraysReached != 0 {
				t.Fatalf("%s: got ResultsReached=%v ResultCount=%d RowsArraysReached=%d, want true/>0/0",
					name, res.ResultsReached, res.ResultCount, res.RowsArraysReached)
			}
			if string(out) != string(in) {
				t.Fatalf("%s: expected original bytes unchanged, got: %s", name, out)
			}
		})
	}

	// Genuinely empty results (present-but-empty or null rows[]) must NOT read as
	// drift: the "rows" key is present, so RowsArraysReached > 0 and RowsSeen == 0.
	empty := map[string][]byte{
		"rows empty array": []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[]}]}},"meta":{}}`),
		"rows null":        []byte(`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":null}]}},"meta":{}}`),
	}
	for name, in := range empty {
		t.Run(name, func(t *testing.T) {
			_, res := InjectRowsWebURL(in, "https://signoz.example.com", "trace", "trace_id")
			if res.RowsArraysReached == 0 || res.RowsSeen != 0 {
				t.Fatalf("%s: got RowsArraysReached=%d RowsSeen=%d, want >0/0 (not drift)",
					name, res.RowsArraysReached, res.RowsSeen)
			}
		})
	}
}
