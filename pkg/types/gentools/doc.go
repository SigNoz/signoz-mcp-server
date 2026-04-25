// Package gentools holds the JSON Schema artifacts for every generated
// MCP tool plus the runtime composer that hands them to mcp-go.
//
// Layout:
//   - components/zz_generated_<Name>.json  — one OpenAPI component schema per file
//   - tools/zz_generated_<tool_name>.json  — one MCP tool schema per file (skeleton with $refs)
//   - tools/zz_generated_<tag>.go          — typed *Input structs (package gentoolstypes)
//   - zz_generated_schemas.go              — //go:embed glue + Schemas map
//
// Components and tools are emitted from the SigNoz OpenAPI spec by
// cmd/gen-tools. At init time, zz_generated_schemas.go walks each tool's
// $refs against the components map and produces a self-contained JSON
// Schema in Schemas[toolName].
package gentools
