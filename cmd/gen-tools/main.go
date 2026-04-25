// Command gen-tools turns the SigNoz OpenAPI spec into MCP tool registrations.
//
// Usage:
//
//	gen-tools \
//	    --spec ../signoz/docs/api/openapi.yml \
//	    --handlers-dir internal/handler/tools \
//	    --types-dir pkg/types/gentools \
//	    --types-import github.com/SigNoz/signoz-mcp-server/pkg/types/gentools \
//	    --manifest manifest.json
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/SigNoz/signoz-mcp-server/internal/gen"
)

func main() {
	var cfg gen.Config
	flag.StringVar(&cfg.SpecPath, "spec", "", "path to openapi.yml")
	flag.StringVar(&cfg.HandlersDir, "handlers-dir", "internal/handler/tools", "destination for generated handler files")
	flag.StringVar(&cfg.RootDir, "root-dir", "pkg/types/gentools", "destination for generated schemas + types (gentools package)")
	flag.StringVar(&cfg.GentoolsImportPath, "gentools-import", "github.com/SigNoz/signoz-mcp-server/pkg/types/gentools", "import path of the gentools package")
	flag.StringVar(&cfg.ManifestPath, "manifest", "manifest.json", "path to manifest.json (empty to skip)")
	flag.StringVar(&cfg.DumpIRPath, "dump-ir", "", "optional path to write the parsed IR as JSON (empty to skip)")
	flag.Parse()

	if cfg.SpecPath == "" {
		fmt.Fprintln(flag.CommandLine.Output(), "--spec is required")
		flag.Usage()
		return
	}

	if err := gen.Run(cfg); err != nil {
		log.Fatalf("gen-tools: %v", err)
	}
	log.Printf("gen-tools: wrote handlers to %s, schemas+types under %s", cfg.HandlersDir, cfg.RootDir)
}
