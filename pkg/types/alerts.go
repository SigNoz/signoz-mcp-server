package types

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

// Alert contains only essential information for list display
type Alert struct {
	Name      string `json:"name"`
	RuleID    string `json:"ruleId"`
	Severity  string `json:"severity"`
	State     string `json:"state"`
	AlertType string `json:"alertType"`
	Disabled  bool   `json:"disabled"`
}

// TriggeredAlert contains information about a currently firing alert
type TriggeredAlert struct {
	Alertname string `json:"alertname"`
	RuleID    string `json:"ruleId"`
	Severity  string `json:"severity"`
	StartsAt  string `json:"startsAt"`
	EndsAt    string `json:"endsAt"`
	State     string `json:"state"`
}

// APIAlertLabels represents labels from the Alertmanager /alerts endpoint
type APIAlertLabels struct {
	Alertname string `json:"alertname"`
	RuleID    string `json:"ruleId"`
	Severity  string `json:"severity"`
}

// APIAlertStatus represents status from the Alertmanager /alerts endpoint
type APIAlertStatus struct {
	State string `json:"state"`
}

// APIAlert represents a single triggered alert from /api/v1/alerts
type APIAlert struct {
	Labels   APIAlertLabels `json:"labels"`
	Status   APIAlertStatus `json:"status"`
	StartsAt string         `json:"startsAt"`
	EndsAt   string         `json:"endsAt"`
}

// APIAlertsResponse is the response from /api/v1/alerts (triggered alerts)
type APIAlertsResponse struct {
	Status string     `json:"status"`
	Data   []APIAlert `json:"data"`
}

// APIAlertRuleLabels represents labels on a configured alert rule
type APIAlertRuleLabels struct {
	Severity string `json:"severity"`
}

// APIAlertRule represents a single configured alert rule from /api/v1/rules
type APIAlertRule struct {
	ID                string             `json:"id"`
	Alert             string             `json:"alert"`
	AlertType         string             `json:"alertType"`
	RuleType          string             `json:"ruleType"`
	State             string             `json:"state"`
	Disabled          bool               `json:"disabled"`
	Labels            APIAlertRuleLabels `json:"labels"`
	PreferredChannels []string           `json:"preferredChannels"`
}

// APIAlertRulesData wraps the rules array
type APIAlertRulesData struct {
	Rules []APIAlertRule `json:"rules"`
}

// APIAlertRulesResponse is the response from /api/v1/rules (configured rules)
type APIAlertRulesResponse struct {
	Status string            `json:"status"`
	Data   APIAlertRulesData `json:"data"`
}
