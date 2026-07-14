package types

import (
	"net/url"
	"strconv"
)

// AlertHistoryRequest carries the query parameters for the v2 rule state-history
// timeline endpoint (GET /api/v2/rules/{id}/history/timeline). v2 replaced the v1
// POST body: the structured `filters` set became a single `filterExpression`
// string, and raw `offset` paging became an opaque `cursor`.
type AlertHistoryRequest struct {
	Start            int64
	End              int64
	State            string // "" omitted; one of inactive|pending|firing|nodata|disabled
	FilterExpression string // "" omitted; v5 query-builder filter expression
	Limit            int
	Order            string // "asc" | "desc"
	Cursor           string // "" omitted; opaque cursor from a prior response's nextCursor
}

// QueryParams renders the request as URL query parameters for the v2 GET endpoint.
// Empty optional values are omitted so the server applies its own defaults.
func (r AlertHistoryRequest) QueryParams() url.Values {
	v := url.Values{}
	v.Set("start", strconv.FormatInt(r.Start, 10))
	v.Set("end", strconv.FormatInt(r.End, 10))
	if r.State != "" {
		v.Set("state", r.State)
	}
	if r.FilterExpression != "" {
		v.Set("filterExpression", r.FilterExpression)
	}
	if r.Limit > 0 {
		v.Set("limit", strconv.Itoa(r.Limit))
	}
	if r.Order != "" {
		v.Set("order", r.Order)
	}
	if r.Cursor != "" {
		v.Set("cursor", r.Cursor)
	}
	return v
}

// Alert contains only essential information
type Alert struct {
	Alertname string `json:"alertname"`
	RuleID    string `json:"ruleId"`
	Severity  string `json:"severity"`
	StartsAt  string `json:"startsAt"`
	EndsAt    string `json:"endsAt"`
	State     string `json:"state"`
	WebURL    string `json:"webUrl,omitempty"`
}

type APIAlertLabels struct {
	Alertname string `json:"alertname"`
	RuleID    string `json:"ruleId"`
	Severity  string `json:"severity"`
}

type APIAlertStatus struct {
	State string `json:"state"`
}

type APIAlert struct {
	Labels   APIAlertLabels `json:"labels"`
	Status   APIAlertStatus `json:"status"`
	StartsAt string         `json:"startsAt"`
	EndsAt   string         `json:"endsAt"`
}

type APIAlertsResponse struct {
	Status string     `json:"status"`
	Data   []APIAlert `json:"data"`
}

// AlertRuleSummary contains the fields needed to discover configured rules.
type AlertRuleSummary struct {
	RuleID      string            `json:"ruleId"`
	Alert       string            `json:"alert"`
	AlertType   string            `json:"alertType"`
	RuleType    string            `json:"ruleType"`
	State       string            `json:"state"`
	Disabled    bool              `json:"disabled"`
	Severity    string            `json:"severity,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   string            `json:"createdAt,omitempty"`
	UpdatedAt   string            `json:"updatedAt,omitempty"`
	WebURL      string            `json:"webUrl,omitempty"`
}

// APIAlertRule mirrors the compact fields used from GET /api/v2/rules.
type APIAlertRule struct {
	ID          string            `json:"id"`
	Alert       string            `json:"alert"`
	AlertType   string            `json:"alertType"`
	RuleType    string            `json:"ruleType"`
	State       string            `json:"state"`
	Disabled    bool              `json:"disabled"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   string            `json:"createdAt"`
	UpdatedAt   string            `json:"updatedAt"`
	CreateAt    string            `json:"createAt"`
	UpdateAt    string            `json:"updateAt"`
}

type APIAlertRulesResponse struct {
	Status string         `json:"status"`
	Data   []APIAlertRule `json:"data"`
}

// ListAlertsParams contains query parameters for the GET /api/v1/alerts endpoint.
type ListAlertsParams struct {
	Active    *bool
	Filter    []string
	Inhibited *bool
	Receiver  string
	Silenced  *bool
}

// QueryParams converts the params to url.Values for the HTTP request.
func (p ListAlertsParams) QueryParams() url.Values {
	v := url.Values{}
	if p.Active != nil {
		v.Set("active", strconv.FormatBool(*p.Active))
	}
	for _, f := range p.Filter {
		v.Add("filter", f)
	}
	if p.Inhibited != nil {
		v.Set("inhibited", strconv.FormatBool(*p.Inhibited))
	}
	if p.Receiver != "" {
		v.Set("receiver", p.Receiver)
	}
	if p.Silenced != nil {
		v.Set("silenced", strconv.FormatBool(*p.Silenced))
	}
	return v
}
