package tools

// Regenerate the zz_generated_*.go MCP tool files from the SigNoz OpenAPI
// spec. Run `make gen` from the repo root to invoke this. The spec path
// assumes that the signoz repo is checked out alongside signoz-mcp-server.
//
//go:generate go run ../../../cmd/gen-tools --spec ../../../../signoz/docs/api/openapi.yml --handlers-dir ../../../internal/handler/tools --root-dir ../../../pkg/types/gentools --gentools-import github.com/SigNoz/signoz-mcp-server/pkg/types/gentools --manifest ../../../manifest.json
