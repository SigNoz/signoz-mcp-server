package util

import (
	"strings"
	"testing"
)

func TestInjectWebURL_PreservesLargeInt64(t *testing.T) {
	// A durationNano-style int64 that exceeds float64's exact-integer range
	// (2^53). A naive Unmarshal->map[string]any->Marshal round-trip would coerce
	// it through float64 and round it; the shallow RawMessage decode must pass
	// it through as verbatim bytes.
	const bigInt = "9007199254740993" // 2^53 + 1, not exactly representable as float64
	in := []byte(`{"data":{"durationNano":` + bigInt + `,"name":"GET /cart"}}`)

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
	in := []byte(`{"data":{"spans":[{"zKey":1,"aKey":2.50}],"durationNano":9007199254740993}}`)
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
	in := []byte(`{"data":{"durationNano":9007199254740993}}`)
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
