package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
)

// logPipelinesLatestVersion is the {version} path segment used for every read.
// The backend route GET /api/v1/logs/pipelines/{version} makes the segment
// REQUIRED, and "latest" resolves to the currently deployed agent config.
const logPipelinesLatestVersion = "latest"

func (h *Handler) RegisterLogPipelineHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering log pipeline handlers")

	listTool := mcp.NewTool("signoz_list_log_pipelines",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants to know which log-ingestion pipelines exist, whether one is enabled, or in what order they run. It returns one paginated summary row per pipeline (id, name, alias, enabled, orderId, description, operatorCount) plus the agent config version and deploy status, so a summary tells you which configuration version you are reading. It deliberately omits each pipeline's filter and operator chain to keep the common listing case cheap: call signoz_get_log_pipeline for one pipeline's complete processor definition. Follow pagination.nextOffset until hasMore=false before concluding a pipeline is absent."),
		mcp.WithString("enabledOnly", mcp.DefaultString("false"), boolOrStringType(), mcp.Description("When true, return only pipelines whose enabled flag is true. Default: false (return every pipeline, enabled or not). Filtering is applied before pagination.")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum pipeline summaries per page. Default: 50; max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of pipeline summaries to skip. Default: 0; use pagination.nextOffset for the next page.")),
	)
	h.addTool(s, listTool, h.handleListLogPipelines)

	getTool := mcp.NewTool("signoz_get_log_pipeline",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants one log-ingestion pipeline's complete definition: its full filter (which logs it applies to) and its ordered config operator chain (regex/json/grok parsers, severity and timestamp parsing, add/remove/move, routers, and their if conditions and on_error settings). Identify the pipeline by id or by name; discover both with signoz_list_log_pipelines. Exactly one of id or name is required, and id wins when both are supplied. This reads the same deployed agent config version as the list tool and never modifies a pipeline."),
		mcp.WithString("id", mcp.Description("Pipeline id from signoz_list_log_pipelines. Takes precedence when name is also supplied.")),
		mcp.WithString("name", mcp.Description("Exact pipeline name from signoz_list_log_pipelines. Matched case-insensitively; the pipeline alias is also accepted. Ignored when id is supplied.")),
	)
	h.addTool(s, getTool, h.handleGetLogPipeline)
}

// logPipelinesDoc is the defensively-parsed shape of the pipelines response.
type logPipelinesDoc struct {
	// body is the object holding the agent-config-version fields plus
	// "pipelines" and "history".
	body map[string]any
	// pipelines holds the raw pipeline objects, passed through as map[string]any
	// so unknown/new upstream fields survive to the client untouched.
	pipelines []any
	// pipelinesFieldPresent distinguishes "this deployment genuinely has zero
	// pipelines" (present, empty) from "the response no longer looks like we
	// expect" (absent). Without it an upstream rename of the "pipelines" key
	// would render as a legitimately empty list and silently degrade.
	pipelinesFieldPresent bool
}

// parseLogPipelinesResponse decodes GET /api/v1/logs/pipelines/{version}
// defensively. This endpoint is undocumented, so we do not bind it to a
// hand-written struct and we tolerate more than one envelope:
//
//   - the current wrapped form {"status":"success","data":{...}} produced by the
//     query-service Respond/writeHttpResponse path, and
//   - a bare unwrapped {...} object, which older/legacy builds and some proxies
//     return.
//
// Anything we cannot recognise FAILS OPEN (we return whatever we did find rather
// than erroring the tool) but never FAILS SILENT: every unexpected shape emits a
// WARN so upstream contract drift is detectable in logs even though no unit test
// fixture can predict it. See CLAUDE.md → "Testing across external contracts".
func (h *Handler) parseLogPipelinesResponse(ctx context.Context, raw json.RawMessage) (*logPipelinesDoc, *mcp.CallToolResult) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse log pipelines response", logpkg.ErrAttr(err))
		return nil, upstreamResponseError("failed to parse response: " + err.Error())
	}

	body := top
	if data, ok := top["data"].(map[string]any); ok {
		body = data
	} else if _, hasData := top["data"]; hasData {
		// "data" exists but is not an object: neither envelope we know.
		h.logger.WarnContext(ctx, "Unexpected log pipelines response shape: data is present but not an object; falling back to the top-level object",
			slog.String("version", logPipelinesLatestVersion))
	} else if _, hasPipelines := top["pipelines"]; !hasPipelines {
		h.logger.WarnContext(ctx, "Unexpected log pipelines response shape: neither a wrapped data object nor a top-level pipelines key",
			slog.String("version", logPipelinesLatestVersion),
			slog.Any("topLevelKeys", sortedKeys(top)))
	}

	doc := &logPipelinesDoc{body: body}
	rawPipelines, present := body["pipelines"]
	switch {
	case !present:
		h.logger.WarnContext(ctx, "Log pipelines response has no pipelines key; upstream contract may have changed",
			slog.String("version", logPipelinesLatestVersion),
			slog.Any("bodyKeys", sortedKeys(body)))
	case rawPipelines == nil:
		// An explicit JSON null is the backend's way of saying "none configured".
		doc.pipelinesFieldPresent = true
	default:
		arr, ok := rawPipelines.([]any)
		if !ok {
			h.logger.WarnContext(ctx, "Log pipelines response has a non-array pipelines value; upstream contract may have changed",
				slog.String("version", logPipelinesLatestVersion),
				slog.String("goType", fmt.Sprintf("%T", rawPipelines)))
			break
		}
		doc.pipelinesFieldPresent = true
		doc.pipelines = arr
	}
	return doc, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (h *Handler) handleListLogPipelines(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	enabledOnly, err := parseTriStateBool(args, "enabledOnly")
	if err != nil {
		h.logger.WarnContext(ctx, "Invalid enabledOnly parameter", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)
	h.logger.DebugContext(ctx, "Tool called: signoz_list_log_pipelines", slog.Int("limit", limit), slog.Int("offset", offset))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	raw, err := client.GetLogPipelines(ctx, logPipelinesLatestVersion)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list log pipelines", err, slog.String("version", logPipelinesLatestVersion))
		return upstreamError(err), nil
	}

	doc, errResult := h.parseLogPipelinesResponse(ctx, raw)
	if errResult != nil {
		return errResult, nil
	}

	summaries := make([]any, 0, len(doc.pipelines))
	for _, item := range doc.pipelines {
		p, ok := item.(map[string]any)
		if !ok {
			h.logger.WarnContext(ctx, "Skipping non-object entry in log pipelines array", slog.String("goType", fmt.Sprintf("%T", item)))
			continue
		}
		if enabledOnly != nil && *enabledOnly {
			if on, _ := p["enabled"].(bool); !on {
				continue
			}
		}
		summaries = append(summaries, logPipelineSummary(p))
	}

	total := len(summaries)
	payload, err := paginate.Wrap(paginate.Array(summaries, offset, limit), total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap log pipelines with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	payload, err = withConfigVersionContext(payload, doc)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to attach log pipeline config version context", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(payload, limitClamped), nil
}

// logPipelineSummary keeps the list response cheap: identity, state, ordering,
// and how many operators the chain has — never the operators themselves.
func logPipelineSummary(p map[string]any) map[string]any {
	summary := map[string]any{}
	for _, key := range []string{"id", "name", "alias", "enabled", "orderId", "description"} {
		if v, ok := p[key]; ok {
			summary[key] = v
		}
	}
	operatorCount := 0
	if cfg, ok := p["config"].([]any); ok {
		operatorCount = len(cfg)
	}
	summary["operatorCount"] = operatorCount
	return summary
}

// withConfigVersionContext adds the agent-config-version fields alongside the
// paginated data so an agent always knows which deployed configuration it read,
// and carries pipelinesFieldPresent through so an empty list is never confused
// with an upstream shape change.
func withConfigVersionContext(payload []byte, doc *logPipelinesDoc) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var envelope map[string]any
	if err := dec.Decode(&envelope); err != nil {
		return nil, err
	}
	if v, ok := doc.body["version"]; ok {
		envelope["version"] = v
	}
	if v, ok := doc.body["deployStatus"]; ok {
		envelope["deployStatus"] = v
	}
	envelope["pipelinesFieldPresent"] = doc.pipelinesFieldPresent
	return json.Marshal(envelope)
}

func (h *Handler) handleGetLogPipeline(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	id, _ := args["id"].(string)
	name, _ := args["name"].(string)
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" && name == "" {
		return validationError("id", `is required when "name" is not provided: supply exactly one of "id" or "name" (discover both with signoz_list_log_pipelines)`), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_log_pipeline", slog.String("id", id), slog.String("name", name))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	raw, err := client.GetLogPipelines(ctx, logPipelinesLatestVersion)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get log pipeline", err, slog.String("version", logPipelinesLatestVersion), slog.String("id", id), slog.String("name", name))
		return upstreamError(err), nil
	}

	doc, errResult := h.parseLogPipelinesResponse(ctx, raw)
	if errResult != nil {
		return errResult, nil
	}

	// There is no get-one-pipeline-by-id route upstream, so selection happens
	// here over the same version payload the list tool reads.
	var match map[string]any
	available := make([]string, 0, len(doc.pipelines))
	for _, item := range doc.pipelines {
		p, ok := item.(map[string]any)
		if !ok {
			continue
		}
		pID, _ := p["id"].(string)
		pName, _ := p["name"].(string)
		pAlias, _ := p["alias"].(string)
		available = append(available, fmt.Sprintf("%s (id=%s)", pName, pID))
		if match != nil {
			continue
		}
		if id != "" {
			if pID == id {
				match = p
			}
			continue
		}
		if strings.EqualFold(pName, name) || strings.EqualFold(pAlias, name) {
			match = p
		}
	}

	if match == nil {
		selector := fmt.Sprintf("id %q", id)
		if id == "" {
			selector = fmt.Sprintf("name %q", name)
		}
		if len(available) == 0 {
			return notFoundError(fmt.Sprintf("No log pipeline matches %s: the %s agent config version has no pipelines configured.", selector, logPipelinesLatestVersion)), nil
		}
		return notFoundError(fmt.Sprintf("No log pipeline matches %s. Available pipelines: %s.", selector, strings.Join(available, ", "))), nil
	}

	payload, err := json.Marshal(match)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal log pipeline", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	return structuredResult(payload), nil
}
