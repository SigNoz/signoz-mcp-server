package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardrail_StructuredResultNotesGoThroughAppendNotes catches a note added
// to a result as a bare text block, which clients that show the model only
// StructuredContent (Claude Code) never see. appendNotes is the one function
// allowed to add result blocks; it also lists the notes in StructuredContent.
func TestGuardrail_StructuredResultNotesGoThroughAppendNotes(t *testing.T) {
	t.Run("detects a direct append and a direct assignment", func(t *testing.T) {
		fileset := token.NewFileSet()
		file, err := parser.ParseFile(fileset, "probe.go", `package tools

func appendNotes(res *Result, notes ...string) {
	res.Content = append(res.Content, nil)
}

func bypass(res *Result) {
	res.Content = append(res.Content, nil)
	res.Content = nil
}
`, 0)
		if err != nil {
			t.Fatalf("parse probe: %v", err)
		}
		if got := resultBlockWritesOutsideAppendNotes(fileset, file); len(got) != 2 {
			t.Fatalf("bypasses found = %v, want the 2 writes in bypass and none in appendNotes", got)
		}
	})

	fileset := token.NewFileSet()
	for _, directory := range []string{".", filepath.Join("..", "..", "docs"), filepath.Join("..", "..", "mcp-server")} {
		paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
		if err != nil {
			t.Fatalf("glob production sources in %s: %v", directory, err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fileset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, position := range resultBlockWritesOutsideAppendNotes(fileset, file) {
				t.Errorf("%s adds a result block outside appendNotes; call appendNotes(ctx, logger, result, notes...) so a structured result also lists the note in StructuredContent", position)
			}
		}
	}
}

// resultBlockWritesOutsideAppendNotes reports every assignment to a .Content
// field outside the appendNotes function. Writes to a single block
// (res.Content[i] = ...) edit existing text and are allowed.
func resultBlockWritesOutsideAppendNotes(fileset *token.FileSet, file *ast.File) []token.Position {
	var found []token.Position
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "appendNotes" {
			continue
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			if assign, ok := node.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if selector, ok := lhs.(*ast.SelectorExpr); ok && selector.Sel.Name == "Content" {
						found = append(found, fileset.Position(lhs.Pos()))
					}
				}
			}
			return true
		})
	}
	return found
}
