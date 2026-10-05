package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// Dispatching a spec to the wrong type used to silently discard queries and formulas.
func FuzzQueryPayloadRoundTrip(f *testing.F) {
	for kind, expression := range []string{`{"http.server.duration_count"}`, "SELECT count() FROM signoz_traces.signoz_index_v3", "A / B * 100", "service.name = 'frontend'", "A => B"} {
		f.Add(uint8(kind), expression, []byte(`{"value":9007199254740993}`), uint16(100))
	}
	f.Add(uint8(0), "up", []byte(`{"compositeQuery":{"queries":[{"type":"promql","spec":"not-an-object"}]}}`), uint16(1))
	f.Fuzz(func(t *testing.T, kind uint8, expression string, raw []byte, bound uint16) {
		if len(expression)+len(raw) > 64<<10 {
			t.Skip()
		}
		// Exercise malformed user payloads as well as deliberately valid specs below.
		var arbitrary QueryPayload
		if err := json.Unmarshal(raw, &arbitrary); err == nil {
			_ = arbitrary.Validate()
		}
		text, err := json.Marshal("x" + expression)
		if err != nil {
			t.Fatal(err)
		}
		limit := int(bound)%10000 + 1
		order := `[{"key":{"name":"__result"},"direction":"desc"}]`
		var queryType, spec string
		switch kind % 5 {
		case 0:
			queryType = "promql"
			spec = fmt.Sprintf(`{"name":"A","query":%s,"legend":%s,"step":"60s","stats":true}`, text, text)
		case 1:
			queryType = "clickhouse_sql"
			spec = fmt.Sprintf(`{"name":"A","query":%s,"legend":%s}`, text, text)
		case 2:
			queryType = "builder_formula"
			spec = fmt.Sprintf(`{"name":"C","expression":%s,"legend":%s,"limit":%d,"order":%s}`, text, text, limit, order)
		case 3:
			queryType = "builder_query"
			spec = fmt.Sprintf(`{"name":"A","signal":"metrics","source":"meter","filter":{"expression":%s},"aggregations":[{"metricName":%s,"spaceAggregation":"avg"}],"limit":%d,"offset":%d,"order":%s,"cursor":%s,"legend":%s,"limitBy":{"keys":["service.name"],"value":"5"}}`, text, text, limit, bound, order, text, text)
		case 4:
			queryType = "builder_trace_operator"
			if !json.Valid(raw) {
				raw = []byte(`null`)
			}
			spec = fmt.Sprintf(`{"name":"T","expression":%s,"returnSpansFrom":"A","extension":%s}`, text, raw)
		}
		input := fmt.Appendf(nil, `{"schemaVersion":"v1","start":1700000000000,"end":1700003600000,"requestType":"time_series","noCache":true,"compositeQuery":{"queries":[{"type":%q,"spec":%s}]}}`, queryType, spec)
		var payload QueryPayload
		if err := json.Unmarshal(input, &payload); err != nil {
			t.Fatal(err)
		}
		if err := payload.Validate(); err != nil {
			t.Fatalf("supported authored payload rejected: %v", err)
		}
		out, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		assertAuthoredQueryFields(t, decodeQueryJSON(t, input), decodeQueryJSON(t, out))
		var again QueryPayload
		if err := json.Unmarshal(out, &again); err != nil {
			t.Fatal(err)
		}
		if err := again.Validate(); err != nil {
			t.Fatalf("round-tripped payload rejected: %v", err)
		}
	})
}

func decodeQueryJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertAuthoredQueryFields(t *testing.T, want, got any) {
	t.Helper()
	switch want := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("authored object became %T", got)
		}
		for key, value := range want {
			actual, exists := object[key]
			if !exists {
				t.Fatalf("authored field %q disappeared", key)
			}
			assertAuthoredQueryFields(t, value, actual)
		}
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(want) {
			t.Fatalf("authored array changed: want %#v, got %#v", want, got)
		}
		for i := range want {
			assertAuthoredQueryFields(t, want[i], array[i])
		}
	default:
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("authored value changed: want %#v, got %#v", want, got)
		}
	}
}
