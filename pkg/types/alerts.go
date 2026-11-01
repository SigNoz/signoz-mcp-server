package types

import (
	"net/url"
	"strconv"
)

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
