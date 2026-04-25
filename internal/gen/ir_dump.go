package gen

import (
	"encoding/json"
	"fmt"
	"os"
)

// DumpIR writes the parsed Operation IR to path as indented JSON. It mirrors
// the intermediate "Provider Code Specification" artifact used by
// hashicorp/terraform-plugin-codegen-openapi: a serializable, reviewable
// picture of the spec-derived metadata that sits between parsing and
// emission. Useful for diffing spec changes, seeding snapshot tests, and
// (future) human overrides.
//
// The JSON shape is the in-memory Operation struct as-is; we accept that we
// may need a more stable serialization format later if we ever publish the
// IR as a public contract.
func DumpIR(path string, ops []Operation) error {
	out := irDoc{
		Operations: ops,
		Summary: irSummary{
			Total:      len(ops),
			ByTag:      countByTag(ops),
			HasEnum:    countOps(ops, func(p Param) bool { return p.Schema != nil && len(p.Schema.Enum) > 0 }),
			HasFormat:  countOps(ops, func(p Param) bool { return p.Schema != nil && p.Schema.Format != "" }),
			HasPattern: countOps(ops, func(p Param) bool { return p.Schema != nil && p.Schema.Pattern != "" }),
			HasRange:   countOps(ops, func(p Param) bool { return p.Schema != nil && (p.Schema.Min != nil || p.Schema.Max != nil) }),
		},
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IR: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

type irDoc struct {
	Summary    irSummary   `json:"summary"`
	Operations []Operation `json:"operations"`
}

type irSummary struct {
	Total      int            `json:"total"`
	ByTag      map[string]int `json:"by_tag"`
	HasEnum    int            `json:"ops_with_enum_param"`
	HasFormat  int            `json:"ops_with_format_param"`
	HasPattern int            `json:"ops_with_pattern_param"`
	HasRange   int            `json:"ops_with_range_param"`
}

func countByTag(ops []Operation) map[string]int {
	m := make(map[string]int)
	for _, o := range ops {
		m[o.Tag]++
	}
	return m
}

func countOps(ops []Operation, pred func(Param) bool) int {
	n := 0
	for _, o := range ops {
		match := false
		for _, p := range append(append([]Param{}, o.PathParams...), o.QueryParams...) {
			if pred(p) {
				match = true
				break
			}
		}
		if match {
			n++
		}
	}
	return n
}
