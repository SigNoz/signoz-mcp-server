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
// component schema reachable (transitively) from any emitted tool. Files
// land in dir, which the parent gentools package //go:embeds.
func EmitComponentFiles(doc *openapi3.T, ops []Operation, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// Seed: the body component name for each op (if any).
	seed := []string{}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.BodyDesc != "" && op.BodySchema != nil {
			if !seen[op.BodyDesc] {
				seen[op.BodyDesc] = true
				seed = append(seed, op.BodyDesc)
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

// EmitToolFiles writes one zz_generated_<tool_name>.json file per Operation.
// Each is a self-describing JSON Schema with $refs pointing at component
// names; the parent gentools package walks these at init to compose the
// final per-tool $defs blocks.
func EmitToolFiles(doc *openapi3.T, ops []Operation, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	keepNames := make([]string, 0, len(ops))
	for _, op := range ops {
		raw, _, err := BuildToolSchemaSkeleton(doc, op)
		if err != nil {
			return fmt.Errorf("%s: %w", op.OperationID, err)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return fmt.Errorf("indent %s: %w", op.ToolName, err)
		}
		pretty.WriteByte('\n')
		path := filepath.Join(dir, "zz_generated_"+op.ToolName+".json")
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			return err
		}
		keepNames = append(keepNames, op.ToolName)
	}
	return pruneStale(dir, "zz_generated_", ".json", keepNames)
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
