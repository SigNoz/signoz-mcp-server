package gentools

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// compose.go holds the only //go:embed for the components directory and the
// runtime composer that turns each tool's skeleton into a self-contained
// JSON Schema. It is hand-written, not generated. Per-tag generated files
// reference ComposeSchema when initializing their Schema<OpID> vars.

//go:embed components/*.json
var componentsFS embed.FS

const refPrefix = "#/$defs/"

// components is loaded once at package init from the embedded JSON files.
// Map key is the OpenAPI component name (filename without the
// zz_generated_ prefix or .json suffix), value is the raw schema bytes.
var components = mustLoadComponents()

func mustLoadComponents() map[string]json.RawMessage {
	entries, err := componentsFS.ReadDir("components")
	if err != nil {
		panic(fmt.Sprintf("gentools: ReadDir components: %v", err))
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fname := e.Name()
		key := strings.TrimSuffix(strings.TrimPrefix(fname, "zz_generated_"), ".json")
		data, err := componentsFS.ReadFile("components/" + fname)
		if err != nil {
			panic(fmt.Sprintf("gentools: ReadFile components/%s: %v", fname, err))
		}
		out[key] = data
	}
	return out
}

// ComposeSchema turns a tool skeleton (a small JSON Schema with $refs into
// the components catalogue) into a self-contained JSON Schema by injecting
// the transitive closure of referenced components as a $defs block. It is
// called at init time from each generated zz_generated_<tag>.go's Schema
// variable initializers, so the cost is paid once per process.
func ComposeSchema(skeleton []byte) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(skeleton, &obj); err != nil {
		panic(fmt.Sprintf("gentools: bad skeleton: %v", err))
	}
	closure := transitiveRefs(obj)
	if len(closure) > 0 {
		defs := make(map[string]json.RawMessage, len(closure))
		for _, name := range closure {
			defs[name] = components[name]
		}
		obj["$defs"] = defs
	}
	out, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Sprintf("gentools: re-marshal: %v", err))
	}
	return out
}

// transitiveRefs walks seed looking for {"$ref": "#/$defs/<Name>"} entries,
// then expands each named component to find its own refs, until the set
// stabilizes. Returns the sorted closure. Refs to missing components are
// dropped silently — codegen is deterministic, so a missing ref means the
// spec changed; downstream JSON Schema validators will surface it.
func transitiveRefs(seed any) []string {
	seenSet := make(map[string]bool)
	var queue []string
	collectRefs(seed, &queue)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seenSet[name] {
			continue
		}
		seenSet[name] = true
		raw, ok := components[name]
		if !ok {
			continue
		}
		var sub any
		if err := json.Unmarshal(raw, &sub); err != nil {
			continue
		}
		var nested []string
		collectRefs(sub, &nested)
		queue = append(queue, nested...)
	}
	out := make([]string, 0, len(seenSet))
	for n := range seenSet {
		out = append(out, n)
	}
	sortStrings(out)
	return out
}

func collectRefs(v any, into *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok {
			if name, found := strings.CutPrefix(ref, refPrefix); found {
				*into = append(*into, name)
			}
		}
		for _, val := range x {
			collectRefs(val, into)
		}
	case []any:
		for _, val := range x {
			collectRefs(val, into)
		}
	}
}

// sortStrings keeps the file self-contained without a "sort" import — the
// component closures are small (~50 entries), so insertion sort is fine.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
