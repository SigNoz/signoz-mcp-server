package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
		mcp.WithOutputSchema[docsindex.SearchResponse](),
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user asks a SigNoz product, setup, instrumentation, configuration, API, deployment, or troubleshooting question and no exact documentation page is selected. Returns ranked official-doc matches with URLs and snippets. Do not use for live tenant data; use signoz_fetch_doc when a result or exact docs URL needs full content."),
		// Not Required() so the legacy "query" alias (#367) stays valid for
		// schema-validating clients; the handler still enforces "is required".
		mcp.WithString("searchText", mcp.Description("Natural-language or keyword query to search in official SigNoz docs.")),
		// limit advertises the ["integer","string"] union via intOrStringType() since
		// parseLimit also accepts a JSON number — a schema-validating client sending
		// {"limit": 3} must not be rejected. The 25 ceiling bounds the in-process bleve
		// index's per-result memory hydration on the shared multi-tenant pod.
		mcp.WithString("limit", mcp.DefaultString("10"), intOrStringType(), mcp.Description("Maximum results to return. Default: 10, max: 25 (capped to bound the docs index's memory footprint).")),
		mcp.WithString("section_slug", mcp.Description(`Optional exact top-level docs section filter, for example "setup", "logs-management", "apm-distributed-tracing", "metrics", "alerts", "dashboards", "signoz-apis", "querying", or "collection-agents".`)),
	)
	h.addTool(s, searchTool, h.handleSearchDocs)

	fetchTool := mcp.NewTool("signoz_fetch_doc",
		mcp.WithOutputSchema[docsindex.FetchResult](),
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this after signoz_search_docs, or when an exact official SigNoz docs URL or /docs/... path is known, to return one page's full Markdown or a requested heading. Do not use it to discover pages or query live tenant data; use signoz_search_docs for topical discovery."),
		mcp.WithString("url", mcp.Required(), mcp.Description("Full https://signoz.io/docs/... URL or /docs/... path.")),
		mcp.WithString("heading", mcp.Description(`Optional heading anchor ID or heading text, for example "prerequisites" or "## Prerequisites".`)),
	)
	h.addTool(s, fetchTool, h.handleFetchDoc)

	sitemap := mcp.NewResource(
		docsindex.DocsSitemapURI,
		"SigNoz Docs Sitemap",
		mcp.WithResourceDescription("Use this resource when an MCP client needs the indexed official SigNoz documentation catalog and page URLs. Use signoz_search_docs for topical discovery and signoz_fetch_doc for page content."),
		mcp.WithMIMEType("text/markdown"),
	)
	h.addResource(s, sitemap, h.handleDocsSitemap)
}

func (h *Handler) handleSearchDocs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.docsIndex == nil || !h.docsIndex.Ready() {
		return docsindex.IndexNotReadyError(), nil
	}
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}
	// Canonical param is "searchText"; "query" is a permanent legacy alias (#367).
	// Read the canonical key first, then fall back to the alias.
	query, _ := args["searchText"].(string)
	if query == "" {
		query, _ = args["query"].(string)
	}
	if query == "" {
		return validationError("searchText", "is required"), nil
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
		fallbackCode := CodeInternalError
		if errors.Is(err, docsindex.ErrInvalidSearchQuery) {
			fallbackCode = CodeValidationFailed
		}
		return errorWithCause(err, fallbackCode, err.Error()), nil
	}
	return structuredToolResult(result)
}

func (h *Handler) handleFetchDoc(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.docsIndex == nil || !h.docsIndex.Ready() {
		return docsindex.IndexNotReadyError(), nil
	}
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return validationError("url", "is required"), nil
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
		return errorWithCause(err, CodeInternalError, err.Error()), nil
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
		return InternalErrorResult(code), nil
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
