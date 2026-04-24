package types

import (
	"net/url"
	"strconv"
)

// AlertHistoryRequest is the request payload for alert history
type AlertHistoryRequest struct {
	Start   int64               `json:"start"`
	End     int64               `json:"end"`
	State   string              `json:"state,omitempty"`
	Offset  int                 `json:"offset"`
	Limit   int                 `json:"limit"`
	Order   string              `json:"order"`
	Filters AlertHistoryFilters `json:"filters"`
}

// AlertHistoryFilters is filters for alert history
type AlertHistoryFilters struct {
	Items []interface{} `json:"items"`
	Op    string        `json:"op"`
}

// Alert contains only essential information
type Alert struct {
	Alertname string `json:"alertname"`
	RuleID    string `json:"ruleId"`
	Severity  string `json:"severity"`
	StartsAt  string `json:"startsAt"`
	EndsAt    string `json:"endsAt"`
	State     string `json:"state"`
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
