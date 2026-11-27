package client

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// GetTraceSummary returns the totals of one trace.
func (s *SigNoz) GetTraceSummary(ctx context.Context, traceID string) (json.RawMessage, error) {
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetTraceSummary(ctx, traceID)
	})
}

// GetTraceHighlights returns the first spans of each view of one trace. No
// views means every view.
func (s *SigNoz) GetTraceHighlights(ctx context.Context, traceID string, views []string, limit int) (json.RawMessage, error) {
	params := &apiclient.GetTraceHighlightsParams{Limit: optInt(limit)}
	if len(views) > 0 {
		typed := make([]apitypes.SpantypesSpanListView, len(views))
		for i, view := range views {
			typed[i] = apitypes.SpantypesSpanListView(view)
		}
		params.Views = &typed
	}
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetTraceHighlights(ctx, traceID, params)
	})
}

// GetTraceSpan returns one span with every attribute, event, and link.
func (s *SigNoz) GetTraceSpan(ctx context.Context, traceID, spanID string) (json.RawMessage, error) {
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetTraceSpan(ctx, traceID, spanID)
	})
}

// ListTraceSpans returns a page of the spans of a trace. With a spanID, scope
// picks the spans around that span: descendants, children, or ancestors. The
// four routes take the same parameters.
func (s *SigNoz) ListTraceSpans(ctx context.Context, traceID, spanID, scope, view, query string, selectFields []string, offset, limit int) (json.RawMessage, error) {
	params := &apiclient.ListTraceSpansParams{
		View:   optEnum[apitypes.SpantypesSpanListView](view),
		Query:  optString(query),
		Offset: optInt(offset),
		Limit:  optInt(limit),
	}
	if len(selectFields) > 0 {
		params.SelectFields = &selectFields
	}
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		switch {
		case spanID == "":
			return api.ListTraceSpans(ctx, traceID, params)
		case scope == "children":
			return api.ListTraceSpanChildren(ctx, traceID, spanID, (*apiclient.ListTraceSpanChildrenParams)(params))
		case scope == "ancestors":
			return api.ListTraceSpanAncestors(ctx, traceID, spanID, (*apiclient.ListTraceSpanAncestorsParams)(params))
		default:
			return api.ListTraceSpanDescendants(ctx, traceID, spanID, (*apiclient.ListTraceSpanDescendantsParams)(params))
		}
	})
}

// GetTraceThread returns a page of the AI conversation of a trace. after is
// the nextCursor of the previous page.
func (s *SigNoz) GetTraceThread(ctx context.Context, traceID string, limit int, after string) (json.RawMessage, error) {
	params := &apiclient.GetTraceThreadParams{Limit: optInt(limit), After: optString(after)}
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetTraceThread(ctx, traceID, params)
	})
}
