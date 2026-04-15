package client

import (
	"context"
	"encoding/json"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// MockClient implements Client for use in unit tests.
// Each method delegates to the corresponding function field when non-nil,
// otherwise returns a default empty JSON object and nil error.
type MockClient struct {
	ListMetricsFn             func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error)
	ListAlertsFn              func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error)
	GetAlertByRuleIDFn        func(ctx context.Context, ruleID string) (json.RawMessage, error)
	GetAlertHistoryFn         func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error)
	ListDashboardsFn          func(ctx context.Context) (json.RawMessage, error)
	GetDashboardFn            func(ctx context.Context, uuid string) (json.RawMessage, error)
	CreateDashboardFn         func(ctx context.Context, dashboard types.Dashboard) (json.RawMessage, error)
	UpdateDashboardFn         func(ctx context.Context, id string, dashboard types.Dashboard) error
	CreateDashboardRawFn      func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error)
	UpdateDashboardRawFn      func(ctx context.Context, id string, dashboardJSON []byte) error
	DeleteDashboardFn         func(ctx context.Context, id string) error
	ListServicesFn            func(ctx context.Context, start, end string) (json.RawMessage, error)
	GetServiceTopOperationsFn func(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error)
	QueryBuilderV5Fn          func(ctx context.Context, body []byte) (json.RawMessage, error)
	ListLogViewsFn            func(ctx context.Context) (json.RawMessage, error)
	GetLogViewFn              func(ctx context.Context, viewID string) (json.RawMessage, error)
	GetFieldKeysFn            func(ctx context.Context, signal, metricName, searchText, fieldContext, fieldDataType, source string) (json.RawMessage, error)
	GetFieldValuesFn          func(ctx context.Context, signal, name, metricName, searchText, source string) (json.RawMessage, error)
	GetTraceDetailsFn         func(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error)
	CreateAlertRuleFn         func(ctx context.Context, alertJSON []byte) (json.RawMessage, error)
}

// Compile-time check that MockClient satisfies Client.
var _ Client = (*MockClient)(nil)

func (m *MockClient) ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
	if m.ListMetricsFn != nil {
		return m.ListMetricsFn(ctx, start, end, limit, searchText, source)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) ListAlerts(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
	if m.ListAlertsFn != nil {
		return m.ListAlertsFn(ctx, params)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetAlertByRuleID(ctx context.Context, ruleID string) (json.RawMessage, error) {
	if m.GetAlertByRuleIDFn != nil {
		return m.GetAlertByRuleIDFn(ctx, ruleID)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetAlertHistory(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
	if m.GetAlertHistoryFn != nil {
		return m.GetAlertHistoryFn(ctx, ruleID, req)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) ListDashboards(ctx context.Context) (json.RawMessage, error) {
	if m.ListDashboardsFn != nil {
		return m.ListDashboardsFn(ctx)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetDashboard(ctx context.Context, uuid string) (json.RawMessage, error) {
	if m.GetDashboardFn != nil {
		return m.GetDashboardFn(ctx, uuid)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) CreateDashboard(ctx context.Context, dashboard types.Dashboard) (json.RawMessage, error) {
	if m.CreateDashboardFn != nil {
		return m.CreateDashboardFn(ctx, dashboard)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) UpdateDashboard(ctx context.Context, id string, dashboard types.Dashboard) error {
	if m.UpdateDashboardFn != nil {
		return m.UpdateDashboardFn(ctx, id, dashboard)
	}
	return nil
}

func (m *MockClient) CreateDashboardRaw(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
	if m.CreateDashboardRawFn != nil {
		return m.CreateDashboardRawFn(ctx, dashboardJSON)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) UpdateDashboardRaw(ctx context.Context, id string, dashboardJSON []byte) error {
	if m.UpdateDashboardRawFn != nil {
		return m.UpdateDashboardRawFn(ctx, id, dashboardJSON)
	}
	return nil
}

func (m *MockClient) DeleteDashboard(ctx context.Context, id string) error {
	if m.DeleteDashboardFn != nil {
		return m.DeleteDashboardFn(ctx, id)
	}
	return nil
}

func (m *MockClient) ListServices(ctx context.Context, start, end string) (json.RawMessage, error) {
	if m.ListServicesFn != nil {
		return m.ListServicesFn(ctx, start, end)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetServiceTopOperations(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error) {
	if m.GetServiceTopOperationsFn != nil {
		return m.GetServiceTopOperationsFn(ctx, start, end, service, tags)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) QueryBuilderV5(ctx context.Context, body []byte) (json.RawMessage, error) {
	if m.QueryBuilderV5Fn != nil {
		return m.QueryBuilderV5Fn(ctx, body)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) ListLogViews(ctx context.Context) (json.RawMessage, error) {
	if m.ListLogViewsFn != nil {
		return m.ListLogViewsFn(ctx)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetLogView(ctx context.Context, viewID string) (json.RawMessage, error) {
	if m.GetLogViewFn != nil {
		return m.GetLogViewFn(ctx, viewID)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetFieldKeys(ctx context.Context, signal, metricName, searchText, fieldContext, fieldDataType, source string) (json.RawMessage, error) {
	if m.GetFieldKeysFn != nil {
		return m.GetFieldKeysFn(ctx, signal, metricName, searchText, fieldContext, fieldDataType, source)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetFieldValues(ctx context.Context, signal, name, metricName, searchText, source string) (json.RawMessage, error) {
	if m.GetFieldValuesFn != nil {
		return m.GetFieldValuesFn(ctx, signal, name, metricName, searchText, source)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) GetTraceDetails(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
	if m.GetTraceDetailsFn != nil {
		return m.GetTraceDetailsFn(ctx, traceID, includeSpans, startTime, endTime)
	}
	return json.RawMessage(`{}`), nil
}

func (m *MockClient) CreateAlertRule(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
	if m.CreateAlertRuleFn != nil {
		return m.CreateAlertRuleFn(ctx, alertJSON)
	}
	return json.RawMessage(`{}`), nil
}
