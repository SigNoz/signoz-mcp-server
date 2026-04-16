package client

import (
	"context"
	"encoding/json"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// Client defines the interface for interacting with the SigNoz API.
// Handler code depends on this interface, enabling mock-based unit testing.
type Client interface {
	ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error)
	ListAlerts(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error)
	GetAlertByRuleID(ctx context.Context, ruleID string) (json.RawMessage, error)
	GetAlertHistory(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error)
	ListDashboards(ctx context.Context) (json.RawMessage, error)
	GetDashboard(ctx context.Context, uuid string) (json.RawMessage, error)
	CreateDashboard(ctx context.Context, dashboard types.Dashboard) (json.RawMessage, error)
	UpdateDashboard(ctx context.Context, id string, dashboard types.Dashboard) error
	CreateDashboardRaw(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error)
	UpdateDashboardRaw(ctx context.Context, id string, dashboardJSON []byte) error
	DeleteDashboard(ctx context.Context, id string) error
	ListServices(ctx context.Context, start, end string) (json.RawMessage, error)
	GetServiceTopOperations(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error)
	QueryBuilderV5(ctx context.Context, body []byte) (json.RawMessage, error)
	ListLogViews(ctx context.Context) (json.RawMessage, error)
	GetLogView(ctx context.Context, viewID string) (json.RawMessage, error)
	GetFieldKeys(ctx context.Context, signal, metricName, searchText, fieldContext, fieldDataType, source string) (json.RawMessage, error)
	GetFieldValues(ctx context.Context, signal, name, metricName, searchText, source string) (json.RawMessage, error)
	GetTraceDetails(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error)
	CreateAlertRule(ctx context.Context, alertJSON []byte) (json.RawMessage, error)
	ListNotificationChannels(ctx context.Context) (json.RawMessage, error)
	CreateNotificationChannel(ctx context.Context, receiverJSON []byte) (json.RawMessage, error)
	UpdateNotificationChannel(ctx context.Context, id string, receiverJSON []byte) (json.RawMessage, error)
	TestNotificationChannel(ctx context.Context, receiverJSON []byte) error
}
