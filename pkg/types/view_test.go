package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSavedView_RoundTripJSON(t *testing.T) {
	src := []byte(`{"id":"019dade7-3edc-79f4-b885-f6fad49722f2","name":"akshay","category":"","sourcePage":"traces","tags":null,"compositeQuery":{"queryType":"builder"},"extraData":"","createdAt":"2026-04-21T10:00:00Z","createdBy":"user@example.com","updatedAt":"2026-04-21T10:00:00Z","updatedBy":"user@example.com"}`)

	var v SavedView
	if err := json.Unmarshal(src, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.ID != "019dade7-3edc-79f4-b885-f6fad49722f2" {
		t.Errorf("ID = %q", v.ID)
	}
	if v.Name != "akshay" {
		t.Errorf("Name = %q", v.Name)
	}
	if v.SourcePage != "traces" {
		t.Errorf("SourcePage = %q", v.SourcePage)
	}
	if string(v.CompositeQuery) != `{"queryType":"builder"}` {
		t.Errorf("CompositeQuery = %s", v.CompositeQuery)
	}

	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back SavedView
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.Name != v.Name || back.SourcePage != v.SourcePage || back.ID != v.ID {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

func TestSavedView_CreatePayload_OmitsServerFields(t *testing.T) {
	v := SavedView{
		Name:           "my view",
		SourcePage:     "logs",
		CompositeQuery: json.RawMessage(`{"queryType":"builder"}`),
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, forbidden := range []string{`"id"`, `"createdAt"`, `"createdBy"`, `"updatedAt"`, `"updatedBy"`, `"tags"`, `"category"`, `"extraData"`} {
		if strings.Contains(s, forbidden) {
			t.Errorf("create payload should omit %s, got: %s", forbidden, s)
		}
	}
}
