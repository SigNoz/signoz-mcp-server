package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

const (
	feedbackMessageMaxChars  = 2000
	feedbackToolNameMaxChars = 128

	feedbackRecordedMessage = "Feedback recorded."
	feedbackDisabledMessage = "Feedback is turned off on this server, so nothing was sent. Do not retry; continue with the user's task."
)

var feedbackCategories = []string{"tool_error", "confusing_output", "missing_capability", "documentation", "other"}

// Feedback is one agent report about this server, after validation.
type Feedback struct {
	Message  string
	Category string
	ToolName string
}

// FeedbackSink delivers an accepted report to the SigNoz team. The identity is
// the caller's, so the sink can attribute the report without another lookup.
type FeedbackSink func(ctx context.Context, identity *signozclient.AnalyticsIdentity, feedback Feedback)

// SetFeedbackSink turns signoz_submit_feedback on. Without a sink the tool
// reports that feedback is off and sends nothing.
func (h *Handler) SetFeedbackSink(sink FeedbackSink) {
	h.feedbackSink = sink
}

func (h *Handler) RegisterFeedbackHandlers(s *mcp.Server) {
	h.logger.Debug("Registering feedback handlers")

	tool := mcp.NewTool("signoz_submit_feedback",
		withCreateToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this to report a problem with this SigNoz MCP server to the SigNoz team: a tool error you could not explain, a confusing description, schema, or result, a missing capability, or unclear documentation. It returns a confirmation that the report was recorded. Report when you hit the problem or worked around it, without waiting for the user to ask, then continue the user's task. Do not report problems in the user's own telemetry or a mistake the error message already corrected; for product questions use signoz_search_docs. Mask personal and sensitive data before sending: replace names, email addresses, IP addresses, hostnames, IDs, credentials, and telemetry values with placeholders such as <email> or <service>. Tell the user in one sentence what you sent."),
		mcp.WithString("message", mcp.Required(), mcp.Description(`What you tried, what you expected, and what happened, in at most 2000 characters. Name the tool and quote the error text. Describe the user's task in general terms and mask personal or sensitive data with placeholders. Example: "signoz_search_logs returned VALIDATION_FAILED for filter service.name = '<service>'; the error did not say which key was invalid."`)),
		mcp.WithString("category", mcp.Enum(feedbackCategories...), mcp.Description("Kind of problem. tool_error: a call failed or returned a wrong result. confusing_output: a description, schema, or result misled you. missing_capability: no tool does what the task needs. documentation: docs or signoz:// resources are wrong or unclear. other: anything else.")),
		mcp.WithString("toolName", mcp.Description("Exact name of the SigNoz tool the report is about, for example signoz_search_logs. Omit it when the report is about no single tool.")),
	)
	h.addTool(s, tool, h.handleSubmitFeedback)
}

func (h *Handler) handleSubmitFeedback(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.feedbackSink == nil {
		return errorWithCode(CodeUnsupported, feedbackDisabledMessage), nil
	}
	feedback, errResult := parseFeedback(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	identity, err := client.GetAnalyticsIdentity(ctx)
	if err != nil {
		// The identity lookup reports rejected credentials as a sentinel, not an
		// HTTP status error, so upstreamError alone would code it UPSTREAM_ERROR.
		// The client already logs the rejection at WARN.
		if errors.Is(err, signozclient.ErrUnauthorized) {
			return errorWithCode(CodeUnauthorized, fmt.Sprintf("%s %s", upstreamErrorPrefix, err.Error())), nil
		}
		h.logUpstreamFailure(ctx, "Failed to resolve the org for agent feedback", err)
		return upstreamError(err), nil
	}

	h.feedbackSink(ctx, identity, feedback)
	h.logger.InfoContext(ctx, "agent feedback submitted",
		slog.String("feedback.category", feedback.Category),
		slog.String("feedback.tool_name", feedback.ToolName),
		slog.Int("feedback.message.chars", utf8.RuneCountInString(feedback.Message)))
	return mcp.NewToolResultText(feedbackRecordedMessage), nil
}

func parseFeedback(rawArgs any) (Feedback, *mcp.CallToolResult) {
	args, errResult := requireArgsMap(rawArgs)
	if errResult != nil {
		return Feedback{}, errResult
	}
	message, errResult := requireStringArg(args, "message")
	if errResult != nil {
		return Feedback{}, errResult
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return Feedback{}, validationError("message", "cannot be empty")
	}
	if utf8.RuneCountInString(message) > feedbackMessageMaxChars {
		return Feedback{}, validationErrorf("message", "must be at most %d characters; shorten it and send it again", feedbackMessageMaxChars)
	}

	category, errResult := optionalFeedbackString(args, "category")
	if errResult != nil {
		return Feedback{}, errResult
	}
	if category != "" && !slices.Contains(feedbackCategories, category) {
		return Feedback{}, validationErrorf("category", "must be one of: %s", strings.Join(feedbackCategories, ", "))
	}

	toolName, errResult := optionalFeedbackString(args, "toolName")
	if errResult != nil {
		return Feedback{}, errResult
	}
	if utf8.RuneCountInString(toolName) > feedbackToolNameMaxChars {
		return Feedback{}, validationErrorf("toolName", "must be at most %d characters; pass only the tool's name", feedbackToolNameMaxChars)
	}

	return Feedback{Message: message, Category: category, ToolName: toolName}, nil
}

// optionalFeedbackString treats a missing or empty value as absent and rejects
// any other type, so a wrong-typed value is never dropped silently.
func optionalFeedbackString(args map[string]any, key string) (string, *mcp.CallToolResult) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", validationError(key, "must be a string")
	}
	return strings.TrimSpace(value), nil
}
