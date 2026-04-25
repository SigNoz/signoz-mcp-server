package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/getkin/kin-openapi/openapi3"
)

// EmitComponentFiles writes one zz_generated_<Name>.json file per OpenAPI
// component schema reachable (transitively) from any emitted tool input
// OR output. Files land in dir, which the parent gentools package
// //go:embeds.
func EmitComponentFiles(doc *openapi3.T, ops []Operation, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// Seed: body component refs and response component refs from every op.
	// Response schemas can also reference components inline (not only via
	// top-level ref) — the closure for those is gathered by the per-tool
	// walks in BuildOutputSchemaSkeleton, which the caller folds in via
	// ClosureOf below.
	seed := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		seed = append(seed, name)
	}
	for _, op := range ops {
		if op.BodyDesc != "" && op.BodySchema != nil {
			add(op.BodyDesc)
		}
		if op.ResponseSchemaRef != "" {
			add(op.ResponseSchemaRef)
		}
		if op.HasResponse && op.ResponseSchema != nil {
			// Walk the response inline to pick up nested $refs.
			c := &converter{doc: doc, preserveRefs: true, seen: map[string]bool{}}
			if _, err := c.convert(op.ResponseSchema); err != nil {
				return nil, err
			}
			for _, r := range c.refs() {
				add(r)
			}
		}
	}
	closure, err := ClosureOf(doc, seed)
	if err != nil {
		return nil, err
	}

	for _, name := range closure {
		raw, err := BuildDefSchema(doc, name)
		if err != nil {
			return nil, fmt.Errorf("def %q: %w", name, err)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return nil, fmt.Errorf("indent %q: %w", name, err)
		}
		pretty.WriteByte('\n')
		path := filepath.Join(dir, "zz_generated_"+name+".json")
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			return nil, err
		}
	}
	if err := pruneStale(dir, "zz_generated_", ".json", closure); err != nil {
		return nil, err
	}
	return closure, nil
}

// EmitToolFiles writes one zz_generated_<tool_name>.json file (the input
// skeleton) and, when the op declares an application/json response, a
// matching zz_generated_<tool_name>.output.json file. Each is a self-
// describing JSON Schema with $refs pointing at component names; the
// parent gentools package walks these at init to compose the final
// per-tool $defs blocks.
func EmitToolFiles(doc *openapi3.T, ops []Operation, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	keepNames := make(map[string]bool, len(ops)*2)
	write := func(suffix string, op Operation, raw json.RawMessage) error {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return fmt.Errorf("indent %s%s: %w", op.ToolName, suffix, err)
		}
		pretty.WriteByte('\n')
		fname := "zz_generated_" + op.ToolName + suffix + ".json"
		keepNames[fname] = true
		return os.WriteFile(filepath.Join(dir, fname), pretty.Bytes(), 0o644)
	}

	for _, op := range ops {
		in, _, err := BuildToolSchemaSkeleton(doc, op)
		if err != nil {
			return fmt.Errorf("%s input: %w", op.OperationID, err)
		}
		if err := write(".input", op, in); err != nil {
			return err
		}

		out, _, err := BuildOutputSchemaSkeleton(doc, op)
		if err != nil {
			return fmt.Errorf("%s output: %w", op.OperationID, err)
		}
		if out != nil {
			if err := write(".output", op, out); err != nil {
				return err
			}
		}
	}
	return pruneStaleSet(dir, "zz_generated_", ".json", keepNames)
}

// pruneStaleSet is like pruneStale but takes a set of full filenames
// instead of base names. Lets callers mix multiple suffix variants
// (.json + .output.json) under one prune call.
func pruneStaleSet(dir, prefix, suffix string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if len(n) < len(prefix)+len(suffix) ||
			n[:len(prefix)] != prefix ||
			n[len(n)-len(suffix):] != suffix {
			continue
		}
		if !keep[n] {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
	return nil
}

// pruneStale removes files in dir whose name matches the (prefix, suffix)
// pair but isn't in keep. Without this, a spec change that drops an op or
// component would leave an orphan file lying around.
func pruneStale(dir, prefix, suffix string, keep []string) error {
	want := make(map[string]bool, len(keep))
	for _, n := range keep {
		want[prefix+n+suffix] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if len(n) < len(prefix)+len(suffix) ||
			n[:len(prefix)] != prefix ||
			n[len(n)-len(suffix):] != suffix {
			continue
		}
		if !want[n] {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
	return nil
}
