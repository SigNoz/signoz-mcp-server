package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

// Adding navigation links must not corrupt telemetry numbers or drop partial rows.
func FuzzWebURLEnrichment(f *testing.F) {
	for _, raw := range [][]byte{
		[]byte(`{"duration_nano":9007199254740993,"spans":[{"zKey":1,"aKey":2.50}]}`),
		rawTracesBody(), []byte(`null`), []byte(`[1,null,{"webUrl":"keep-me"}]`), []byte(`not json`),
	} {
		f.Add(raw, "abc-123")
	}
	f.Fuzz(func(t *testing.T, raw []byte, id string) {
		if len(raw)+len(id) > 64<<10 {
			t.Skip()
		}
		const base = "https://signoz.example.com"
		rows, _ := InjectRowsWebURL(raw, base, "trace", "trace_id")
		for _, got := range [][]byte{
			InjectWebURL(raw, base, "trace", id),
			InjectListWebURL(raw, base, "trace", "items", "trace_id"), rows,
		} {
			if json.Valid(raw) {
				if !json.Valid(got) {
					t.Fatalf("enrichment produced invalid JSON: %q", got)
				}
			} else if !bytes.Equal(raw, got) {
				t.Fatal("malformed response was changed")
			}
		}
		if !json.Valid(raw) {
			return
		}

		// Keep successful enrichment reachable even as arbitrary JSON shapes mutate.
		encodedID, err := json.Marshal("trace-" + id)
		if err != nil {
			t.Fatal(err)
		}
		var wireID string
		if err := json.Unmarshal(encodedID, &wireID); err != nil {
			t.Fatal(err)
		}
		link, err := json.Marshal(base + "/trace/" + url.PathEscape(wireID))
		if err != nil {
			t.Fatal(err)
		}
		entry := fmt.Sprintf(`{"trace_id":%s,"payload":%s}`, encodedID, raw)
		enriched := fmt.Sprintf(`{"trace_id":%s,"payload":%s,"webUrl":%s}`, encodedID, raw, link)
		for _, wrapped := range []bool{false, true} {
			wrap := func(body string) []byte {
				if wrapped {
					return fmt.Appendf(nil, `{"data":%s,"meta":%s}`, body, raw)
				}
				return []byte(body)
			}
			assertEnrichmentJSON(t, wrap(enriched), InjectWebURL(wrap(entry), base, "trace", wireID))
			list := fmt.Sprintf(`{"items":[%s,null,{"payload":%s}],"total":3}`, entry, raw)
			want := fmt.Sprintf(`{"items":[%s,null,{"payload":%s}],"total":3}`, enriched, raw)
			assertEnrichmentJSON(t, wrap(want), InjectListWebURL(wrap(list), base, "trace", "items", "trace_id"))
		}
		rowEnvelope := func(entry string) []byte {
			return fmt.Appendf(nil, `{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[{"timestamp":"2026-06-19T10:00:00Z","data":%s},{"data":{"payload":%s}},null]}]},"meta":%s}}`, entry, raw, raw)
		}
		got, result := InjectRowsWebURL(rowEnvelope(entry), base, "trace", "trace_id")
		assertEnrichmentJSON(t, rowEnvelope(enriched), got)
		if result.RowsSeen != 3 || result.RowsEnriched != 1 {
			t.Fatalf("partial row counts changed: %+v", result)
		}
	})
}

func assertEnrichmentJSON(t *testing.T, want, got []byte) {
	t.Helper()
	decode := func(raw []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(want), decode(got)) {
		t.Fatalf("enrichment changed fields outside the intended webUrl: want %s, got %s", want, got)
	}
}
