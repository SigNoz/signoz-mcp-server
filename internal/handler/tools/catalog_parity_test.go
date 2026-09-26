package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestOfficialDescriptorConversionMatchesFrozenToolCatalog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "mcp-server", "testdata", "wire-catalog", "tools-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Response struct {
			Result struct {
				Tools []map[string]any `json:"tools"`
			} `json:"result"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}

	registered := registeredTestTools(t)
	got := make([]map[string]any, 0, len(registered))
	for _, entry := range registered {
		b, err := json.Marshal(entry.Tool)
		if err != nil {
			t.Fatal(err)
		}
		var descriptor map[string]any
		if err := json.Unmarshal(b, &descriptor); err != nil {
			t.Fatal(err)
		}
		got = append(got, descriptor)
	}
	sort.Slice(got, func(i, j int) bool { return got[i]["name"].(string) < got[j]["name"].(string) })
	sort.Slice(fixture.Response.Result.Tools, func(i, j int) bool {
		return fixture.Response.Result.Tools[i]["name"].(string) < fixture.Response.Result.Tools[j]["name"].(string)
	})

	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(fixture.Response.Result.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		if len(got) != len(fixture.Response.Result.Tools) {
			t.Fatalf("official descriptor conversion changed frozen tool count: got %d, want %d", len(got), len(fixture.Response.Result.Tools))
		}
		for i := range got {
			g, _ := json.Marshal(got[i])
			w, _ := json.Marshal(fixture.Response.Result.Tools[i])
			if string(g) != string(w) {
				t.Fatalf("official descriptor conversion changed frozen tool %q\n got: %s\nwant: %s", got[i]["name"], g, w)
			}
		}
		t.Fatal("official descriptor conversion changed frozen tool catalog")
	}
}
