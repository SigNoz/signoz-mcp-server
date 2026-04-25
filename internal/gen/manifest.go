package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type toolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SyncManifest merges generated tools into manifest.json in-place. Curated
// entries (present before the merge) are preserved verbatim and retain their
// order; generated entries (keyed by name) are updated or appended and sorted
// alphabetically among themselves so the output is deterministic.
//
// The manifest's top-level key order is preserved by streaming it through an
// ordered map: encoding/json guarantees that ordering when we unmarshal into
// json.RawMessage fields. We only replace the "tools" array contents.
func SyncManifest(path string, ops []Operation) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Pass 1 — decode into an ordered slice of key/value pairs so we can round
	// trip with the original field order.
	pairs, err := decodeTopLevel(raw)
	if err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}

	// Find tools array and parse existing entries.
	var existing []toolEntry
	for _, p := range pairs {
		if p.Key == "tools" {
			if err := json.Unmarshal(p.Value, &existing); err != nil {
				return fmt.Errorf("decode tools: %w", err)
			}
			break
		}
	}

	byName := make(map[string]toolEntry, len(existing))
	curatedOrder := make([]string, 0, len(existing))
	for _, e := range existing {
		if _, seen := byName[e.Name]; !seen {
			curatedOrder = append(curatedOrder, e.Name)
		}
		byName[e.Name] = e
	}

	// Generated tools. Deterministic order by tool name.
	genOps := make([]Operation, len(ops))
	copy(genOps, ops)
	sort.Slice(genOps, func(i, j int) bool { return genOps[i].ToolName < genOps[j].ToolName })

	generatedNames := make([]string, 0, len(genOps))
	for _, o := range genOps {
		desc := o.Summary
		if desc == "" {
			desc = fmt.Sprintf("%s %s", o.Method, o.Path)
		}
		// Only add if not already curated.
		if _, curated := byName[o.ToolName]; curated {
			continue
		}
		byName[o.ToolName] = toolEntry{Name: o.ToolName, Description: desc}
		generatedNames = append(generatedNames, o.ToolName)
	}

	final := make([]toolEntry, 0, len(byName))
	for _, n := range curatedOrder {
		final = append(final, byName[n])
	}
	for _, n := range generatedNames {
		final = append(final, byName[n])
	}

	newTools, err := json.MarshalIndent(final, "  ", "  ")
	if err != nil {
		return err
	}

	// Rewrite pairs.
	for i, p := range pairs {
		if p.Key == "tools" {
			pairs[i].Value = newTools
		}
	}

	out, err := encodeTopLevel(pairs)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

type kv struct {
	Key   string
	Value json.RawMessage
}

// decodeTopLevel streams the top-level object in raw and returns its entries
// in source order. It tolerates the standard MarshalIndent whitespace used by
// encoding/json as well as the two-space indent already in manifest.json.
func decodeTopLevel(raw []byte) ([]kv, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected top-level object, got %v", tok)
	}
	var out []kv
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %v", kt)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, kv{Key: k, Value: raw})
	}
	return out, nil
}

// encodeTopLevel writes pairs back as a two-space indented JSON object with a
// trailing newline. Non-tools values pass through via json.Indent so their
// original key ordering and content survive the round-trip; only the rebuilt
// tools array goes through json.MarshalIndent from typed data.
func encodeTopLevel(pairs []kv) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, p := range pairs {
		key, err := json.Marshal(p.Key)
		if err != nil {
			return nil, err
		}
		buf.WriteString("  ")
		buf.Write(key)
		buf.WriteString(": ")
		// Normalize indent to 2-space with a 2-space prefix. json.Indent
		// preserves the original byte content (key order, values) aside from
		// whitespace — perfect for the manifest's non-tools fields.
		var indented bytes.Buffer
		if err := json.Indent(&indented, p.Value, "  ", "  "); err != nil {
			return nil, err
		}
		buf.Write(indented.Bytes())
		if i < len(pairs)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
