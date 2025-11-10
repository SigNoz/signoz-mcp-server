package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

const (
	SignozApiKey = "SIGNOZ-API-KEY"
	ContentType  = "Content-Type"
)

type SigNoz struct {
	baseURL string
	apiKey  string
	logger  *zap.Logger
}

func NewClient(log *zap.Logger, url, apiKey string) *SigNoz {
	return &SigNoz{logger: log, baseURL: url, apiKey: apiKey}
}

func (s *SigNoz) ListMetricKeys(ctx context.Context) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/metrics/filters/keys", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Making request to SigNoz API", zap.String("method", "GET"), zap.String("endpoint", "/api/v1/metrics/filters/keys"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	s.logger.Debug("Successfully retrieved metric keys", zap.String("url", url), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) SearchMetricByText(ctx context.Context, searchText string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v3/autocomplete/aggregate_attributes?dataSource=metrics&searchText=%s", s.baseURL, searchText)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Searching metric names (aggregate_attributes)", zap.String("searchText", searchText))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("searchText", searchText), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	s.logger.Debug("Successfully searched metric names", zap.String("searchText", searchText), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) ListAlerts(ctx context.Context) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/alerts", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching alerts from SigNoz")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	s.logger.Debug("Successfully retrieved alerts", zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) GetAlertByRuleID(ctx context.Context, ruleID string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/rules/%s", s.baseURL, ruleID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching alert rule details", zap.String("ruleID", ruleID))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("ruleID", ruleID), zap.Error(err))
		return nil, fmt.Errorf("request error: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("read error: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	s.logger.Debug("Successfully retrieved alert rule", zap.String("ruleID", ruleID), zap.Int("status", resp.StatusCode))
	return body, nil
}

// ListDashboards filters data as it returns too much of data even the ui tags
// so we filter and only return required information which might help to get
// detailed info of a dashboard.
func (s *SigNoz) ListDashboards(ctx context.Context) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/dashboards", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching dashboards from SigNoz")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		s.logger.Error("Failed to parse response", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if data, ok := rawResponse["data"].([]interface{}); ok {
		simplifiedDashboards := make([]map[string]interface{}, 0, len(data))

		for _, dashboard := range data {
			if dash, ok := dashboard.(map[string]interface{}); ok {
				var (
					dashData map[string]interface{}
					name     any
					desc     any
					tags     any
				)
				if v, ok := dash["data"].(map[string]interface{}); ok {
					dashData = v
					name = dashData["title"]
					desc = dashData["description"]
					tags = dashData["tags"]
				}

				simplified := map[string]interface{}{
					"uuid":        dash["id"],
					"name":        name,
					"description": desc,
					"tags":        tags,
					"createdAt":   dash["createdAt"],
					"updatedAt":   dash["updatedAt"],
					"createdBy":   dash["createdBy"],
					"updatedBy":   dash["updatedBy"],
				}
				simplifiedDashboards = append(simplifiedDashboards, simplified)
			}
		}

		simplifiedResponse := map[string]interface{}{
			"status": rawResponse["status"],
			"data":   simplifiedDashboards,
		}

		simplifiedJSON, err := json.Marshal(simplifiedResponse)
		if err != nil {
			s.logger.Error("Failed to marshal simplified response", zap.Error(err))
			return nil, fmt.Errorf("failed to marshal simplified response: %w", err)
		}

		s.logger.Debug("Successfully retrieved and simplified dashboards", zap.Int("count", len(simplifiedDashboards)), zap.Int("status", resp.StatusCode))
		return simplifiedJSON, nil
	}

	s.logger.Debug("Successfully retrieved dashboards", zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) GetDashboard(ctx context.Context, uuid string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/dashboards/%s", s.baseURL, uuid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching dashboard details", zap.String("uuid", uuid))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("uuid", uuid), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	s.logger.Debug("Successfully retrieved dashboard", zap.String("uuid", uuid), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) ListServices(ctx context.Context, start, end string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/services", s.baseURL)

	payload := map[string]string{
		"start": start,
		"end":   end,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	s.logger.Debug("Fetching services from SigNoz", zap.String("start", start), zap.String("end", end))

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("start", start), zap.String("end", end), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	s.logger.Debug("Successfully retrieved services", zap.String("start", start), zap.String("end", end), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) GetServiceTopOperations(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/service/top_operations", s.baseURL)

	payload := map[string]any{
		"start":   start,
		"end":     end,
		"service": service,
		"tags":    tags,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	s.logger.Debug("Fetching service top operations", zap.String("start", start), zap.String("end", end), zap.String("service", service))

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("start", start), zap.String("end", end), zap.String("service", service), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	s.logger.Debug("Successfully retrieved service top operations", zap.String("start", start), zap.String("end", end), zap.String("service", service), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) QueryBuilderV5(ctx context.Context, body []byte) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v5/query_range", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("sending request", zap.String("url", url), zap.ByteString("body", body))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(b))
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return b, nil
}

func (s *SigNoz) GetAlertHistory(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/rules/%s/history/timeline", s.baseURL, ruleID)
	// includes ruleid to get history
	// eg: /api/v1/rules/<ruleID>/history/timeline

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set(ContentType, "application/json")
	httpReq.Header.Set(SignozApiKey, s.apiKey)

	s.logger.Debug("sending request", zap.String("url", url), zap.ByteString("body", reqBody))

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	httpReq = httpReq.WithContext(ctx)

	s.logger.Debug("Making request to SigNoz API",
		zap.String("method", "POST"),
		zap.String("endpoint", fmt.Sprintf("/api/v1/rules/%s/history/timeline", ruleID)))

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.Error(err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Error("API request failed",
			zap.String("url", url),
			zap.Int("status", resp.StatusCode),
			zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (s *SigNoz) ListLogViews(ctx context.Context) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/explorer/views?sourcePage=logs", s.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching log views from SigNoz")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	s.logger.Debug("Successfully retrieved log views", zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) GetLogView(ctx context.Context, viewID string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/explorer/views/%s", s.baseURL, viewID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching log view details", zap.String("viewID", viewID))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("viewID", viewID), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	s.logger.Debug("Successfully retrieved log view", zap.String("viewID", viewID), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) GetTraceFieldValues(ctx context.Context, fieldName string, searchText string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/api/v1/fields/values?signal=traces&name=%s&searchText=%s&metricName=&source=meter", s.baseURL, fieldName, searchText)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(SignozApiKey, s.apiKey)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	s.logger.Debug("Fetching trace field values", zap.String("fieldName", fieldName), zap.String("searchText", searchText))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("HTTP request failed", zap.String("url", url), zap.String("fieldName", fieldName), zap.Error(err))
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.logger.Warn("Failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		s.logger.Error("API request failed", zap.String("url", url), zap.Int("status", resp.StatusCode), zap.String("response", string(body)))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("Failed to read response body", zap.String("url", url), zap.Error(err))
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	s.logger.Debug("Successfully retrieved trace field values", zap.String("fieldName", fieldName), zap.Int("status", resp.StatusCode))
	return body, nil
}

func (s *SigNoz) GetTraceDetails(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
	if startTime == 0 || endTime == 0 {
		return nil, fmt.Errorf("start and end time parameters are required")
	}

	filterExpression := fmt.Sprintf("traceID = '%s'", traceID)
	limit := 1000

	queryPayload := types.BuildTracesQueryPayload(startTime, endTime, filterExpression, limit)
	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query payload: %w", err)
	}

	return s.QueryBuilderV5(ctx, queryJSON)
}

func (s *SigNoz) GetTraceErrorAnalysis(ctx context.Context, startTime, endTime int64, serviceName string) (json.RawMessage, error) {
	filterExpression := "hasError = true"
	if serviceName != "" {
		filterExpression += fmt.Sprintf(" AND service.name in ['%s']", serviceName)
	}

	limit := 1000
	queryPayload := types.BuildTracesQueryPayload(startTime, endTime, filterExpression, limit)
	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query payload: %w", err)
	}

	return s.QueryBuilderV5(ctx, queryJSON)
}

func (s *SigNoz) GetTraceSpanHierarchy(ctx context.Context, traceID string, startTime, endTime int64) (json.RawMessage, error) {
	if startTime == 0 || endTime == 0 {
		return nil, fmt.Errorf("start and end time parameters are required")
	}

	filterExpression := fmt.Sprintf("traceID = '%s'", traceID)
	limit := 1000
	queryPayload := types.BuildTracesQueryPayload(startTime, endTime, filterExpression, limit)
	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query payload: %w", err)
	}

	return s.QueryBuilderV5(ctx, queryJSON)
}
