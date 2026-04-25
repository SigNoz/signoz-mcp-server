// Package gen turns the SigNoz OpenAPI spec into MCP tool registrations.
//
// The generator is invoked via cmd/gen-tools (wired into `make gen` and a
// //go:generate directive in internal/handler/tools). Its output lives in
// zz_generated_*.go files under internal/handler/tools and pkg/types/gentools,
// plus the regenerated "tools" array in manifest.json. Generated code does
// not replace curated handlers — when a generated tool name collides with a
// curated one, the generator skips emission.
package gen

// Config bundles the filesystem locations the generator writes to.
//
// The output layout is:
//
//	<HandlersDir>/zz_generated_<tag>.go            (package tools)
//	<RootDir>/zz_generated_<tag>.go                (package gentools — *Input + Schema vars)
//	<RootDir>/components/zz_generated_<Name>.json  (one per OpenAPI component)
//	<RootDir>/tools/zz_generated_<tool>.json       (one per MCP tool)
//
// The hand-written <RootDir>/compose.go (NOT generated) holds the
// //go:embed for components/ and the ComposeSchema function the generated
// per-tag files call to populate their Schema vars.
type Config struct {
	SpecPath           string
	HandlersDir        string
	RootDir            string // pkg/types/gentools
	GentoolsImportPath string // import path of the gentools package
	ManifestPath       string
	DumpIRPath         string
}

// Run parses the spec at cfg.SpecPath, emits generated handler and type files
// into the configured directories, and refreshes the tools array in
// manifest.json. The function is deterministic — two runs against the same
// spec produce byte-identical output.
func Run(cfg Config) error {
	ops, doc, err := Load(cfg.SpecPath)
	if err != nil {
		return err
	}
	if cfg.DumpIRPath != "" {
		if err := DumpIR(cfg.DumpIRPath, ops); err != nil {
			return err
		}
	}

	componentsDir := cfg.RootDir + "/components"
	toolsDir := cfg.RootDir + "/tools"

	// Emit JSON files. Components are the transitive closure of all body
	// schemas; tool files are one-per-op with $refs into components/.
	if _, err := EmitComponentFiles(doc, ops, componentsDir); err != nil {
		return err
	}
	if err := EmitToolFiles(doc, ops, toolsDir); err != nil {
		return err
	}

	// Emit Go: handler files plus per-tag *Input + Schema files at RootDir.
	if err := Emit(ops, cfg.HandlersDir, cfg.RootDir, cfg.GentoolsImportPath); err != nil {
		return err
	}

	if cfg.ManifestPath != "" {
		if err := SyncManifest(cfg.ManifestPath, ops); err != nil {
			return err
		}
	}
	return nil
}
