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

// CreateAlertRuleRequest is the payload for creating a new alert rule.
type CreateAlertRuleRequest struct {
	Alert           string            `json:"alert"`
	AlertType       string            `json:"alertType"`
	RuleType        string            `json:"ruleType"`
	Condition       AlertCondition    `json:"condition"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	EvalWindow      string            `json:"evalWindow"`
	Source          string            `json:"source"`
	Disabled        bool              `json:"disabled"`
	BroadcastToAll  bool              `json:"broadcastToAll"`
	PreferredChannels []string        `json:"preferredChannels,omitempty"`
}

// AlertCondition defines the threshold condition for an alert.
type AlertCondition struct {
	CompositeQuery CompositeAlertQuery `json:"compositeQuery"`
	Op             string              `json:"op"`
	Target         float64             `json:"target"`
	MatchType      string              `json:"matchType"`
	TargetUnit     string              `json:"targetUnit,omitempty"`
	SelectedQueryName string           `json:"selectedQueryName,omitempty"`
}

// CompositeAlertQuery defines the query structure for alert rules.
type CompositeAlertQuery struct {
	BuilderQueries map[string]AlertBuilderQuery `json:"builderQueries"`
	QueryType      string                       `json:"queryType"`
	PanelType      string                       `json:"panelType"`
	Unit           string                       `json:"unit,omitempty"`
}

// AlertBuilderQuery is a single query within a composite alert query.
type AlertBuilderQuery struct {
	QueryName          string                 `json:"queryName"`
	StepInterval       int                    `json:"stepInterval"`
	DataSource         string                 `json:"dataSource"`
	AggregateOperator  string                 `json:"aggregateOperator"`
	AggregateAttribute AggregateAttribute     `json:"aggregateAttribute"`
	Filters            AlertFilters           `json:"filters"`
	Expression         string                 `json:"expression"`
	Disabled           bool                   `json:"disabled"`
	Having             []interface{}          `json:"having"`
	Legend             string                 `json:"legend"`
	Limit              int                    `json:"limit,omitempty"`
	Offset             int                    `json:"offset,omitempty"`
	PageSize           int                    `json:"pageSize,omitempty"`
	OrderBy            []OrderByItem          `json:"orderBy,omitempty"`
	ReduceTo           string                 `json:"reduceTo,omitempty"`
	SpaceAggregation   string                 `json:"spaceAggregation,omitempty"`
	TimeAggregation    string                 `json:"timeAggregation,omitempty"`
	ShiftBy            int                    `json:"ShiftBy,omitempty"`
	Functions          []interface{}          `json:"functions,omitempty"`
}

// AggregateAttribute defines the attribute to aggregate on.
type AggregateAttribute struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
	IsJSON   bool   `json:"isJSON"`
}

// AlertFilters contains filter items for alert queries.
type AlertFilters struct {
	Items []AlertFilterItem `json:"items"`
	Op    string            `json:"op"`
}

// AlertFilterItem is a single filter condition.
type AlertFilterItem struct {
	Key   FilterKey   `json:"key"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}

// FilterKey defines the key for a filter item.
type FilterKey struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
	IsJSON   bool   `json:"isJSON"`
}

// OrderByItem defines ordering for query results.
type OrderByItem struct {
	ColumnName string `json:"columnName"`
	Order      string `json:"order"`
}
