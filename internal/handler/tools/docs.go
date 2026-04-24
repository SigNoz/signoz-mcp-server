package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
)

func (h *Handler) RegisterDocsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering docs handlers")

	searchTool := mcp.NewTool("signoz_search_docs",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Search official SigNoz documentation with BM25 over full markdown content. Use this for ANY SigNoz product question: how-to, feature usage, setup, config, API, deployment, instrumentation, OpenTelemetry integration with SigNoz, and troubleshooting. Call before data tools for ambiguous how-to questions, and after data tools when live telemetry results are confusing. Do not use for fetching actual telemetry, live alert state, or dashboard contents."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language or keyword query to search in official SigNoz docs.")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return. Default 10, max 25.")),
		mcp.WithString("section_slug", mcp.Description(`Optional exact top-level docs section filter, for example "install", "logs-management", "traces", "metrics", "alerts-management", or "dashboards".`)),
	)
	s.AddTool(searchTool, h.handleSearchDocs)

	fetchTool := mcp.NewTool("signoz_fetch_doc",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Fetch full markdown for one official SigNoz documentation page from the local docs index. Use after signoz_search_docs when a result needs detail, exact commands, prerequisites, or a specific section. Accepts only signoz.io/docs URLs or /docs/... paths."),
		mcp.WithString("url", mcp.Required(), mcp.Description("Full https://signoz.io/docs/... URL or /docs/... path.")),
		mcp.WithString("heading", mcp.Description(`Optional heading anchor ID or heading text, for example "prerequisites" or "## Prerequisites".`)),
	)
	s.AddTool(fetchTool, h.handleFetchDoc)

	sitemap := mcp.NewResource(
		docsindex.DocsSitemapURI,
		"SigNoz Docs Sitemap",
		mcp.WithResourceDescription("Indexed SigNoz docs sitemap used by signoz_search_docs and signoz_fetch_doc."),
		mcp.WithMIMEType("text/markdown"),
	)
	s.AddResource(sitemap, h.handleDocsSitemap)
}

func (h *Handler) handleSearchDocs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.docsIndex == nil || !h.docsIndex.Ready() {
		return docsindex.IndexNotReadyError(), nil
	}
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}
	query, _ := args["query"].(string)
	if query == "" {
		return mcp.NewToolResultError(`parameter validation failed: "query" is required`), nil
	}
	sectionSlug, _ := args["section_slug"].(string)
	limit := parseLimit(args["limit"], 10)
	h.logger.DebugContext(ctx, "Tool called: signoz_search_docs",
		slog.String("query", query),
		slog.String("section_slug", sectionSlug),
		slog.Int("limit", limit))

	start := time.Now()
	result, err := h.docsIndex.Search(ctx, query, sectionSlug, limit)
	if h.meters != nil {
		bucket := "0"
		switch {
		case len(result.Results) >= 10:
			bucket = "10+"
		case len(result.Results) >= 5:
			bucket = "5-9"
		case len(result.Results) >= 1:
			bucket = "1-4"
		}
		h.meters.DocsSearches.Add(ctx, 1, metric.WithAttributes(attribute.String("result_count_bucket", bucket)))
		h.meters.DocsSearchDuration.Record(ctx, time.Since(start).Seconds())
	}
	if err != nil {
		if err.Error() == docsindex.CodeIndexNotReady {
			return docsindex.IndexNotReadyError(), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	return structuredToolResult(result)
}

func (h *Handler) handleFetchDoc(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.docsIndex == nil || !h.docsIndex.Ready() {
		return docsindex.IndexNotReadyError(), nil
	}
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format: expected JSON object"), nil
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return mcp.NewToolResultError(`parameter validation failed: "url" is required`), nil
	}
	heading, _ := args["heading"].(string)
	h.logger.DebugContext(ctx, "Tool called: signoz_fetch_doc",
		slog.String("url", rawURL),
		slog.String("heading", heading))

	result, code, err := h.docsIndex.FetchDoc(ctx, rawURL, heading)
	if h.meters != nil && err == nil && code == "" {
		h.meters.DocsFetches.Add(ctx, 1, metric.WithAttributes(attribute.Bool("cached", true)))
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	switch code {
	case "":
		return structuredToolResult(result)
	case docsindex.CodeOutOfScopeURL:
		return docsindex.OutOfScopeURLError(rawURL), nil
	case docsindex.CodeDocNotFound:
		return docsindex.DocNotFoundError(rawURL), nil
	case docsindex.CodeHeadingMissing:
		return docsindex.HeadingNotFoundError(heading, result.AvailableHeadings), nil
	case docsindex.CodeIndexNotReady:
		return docsindex.IndexNotReadyError(), nil
	default:
		return mcp.NewToolResultError(code), nil
	}
}

func (h *Handler) handleDocsSitemap(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	if h.docsIndex == nil || !h.docsIndex.Ready() {
		return nil, fmt.Errorf("docs index is not ready")
	}
	snapshot, ok := h.docsIndex.Snapshot()
	if !ok {
		return nil, fmt.Errorf("docs index is not ready")
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     snapshot.SitemapRaw,
		},
	}, nil
}

func structuredToolResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultStructured(v, string(b)), nil
}

func parseLimit(v any, fallback int) int {
	switch typed := v.(type) {
	case nil:
		return fallback
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}
