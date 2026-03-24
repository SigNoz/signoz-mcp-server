package types

import (
	"net/url"
	"strconv"
)

// AlertHistoryRequest is the request payload for alert history
type AlertHistoryRequest struct {
	Start   int64               `json:"start"`
	End     int64               `json:"end"`
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
