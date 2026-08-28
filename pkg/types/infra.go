package types

// HostListRequest is the request payload for listing infrastructure hosts
type HostListRequest struct {
	Start   int64          `json:"start"`
	End     int64          `json:"end"`
	Filters HostListFilter `json:"filters"`
	GroupBy []any          `json:"groupBy"`
	OrderBy *HostOrderBy   `json:"orderBy,omitempty"`
	Offset  int            `json:"offset"`
	Limit   int            `json:"limit"`
}

// HostListFilter is the filter for host list queries
type HostListFilter struct {
	Items []any  `json:"items"`
	Op    string `json:"op"`
}

// HostOrderBy specifies sorting for host list
type HostOrderBy struct {
	ColumnName string `json:"columnName"`
	Order      string `json:"order"`
}

// HostRecord represents a single host from the infrastructure monitoring response
type HostRecord struct {
	HostName string            `json:"hostName"`
	Active   bool              `json:"active"`
	OS       string            `json:"os"`
	CPU      float64           `json:"cpu"`
	Memory   float64           `json:"memory"`
	Wait     float64           `json:"wait"`
	Load15   float64           `json:"load15"`
	Meta     map[string]string `json:"meta"`
}

// HostListData is the data field of the host list response
type HostListData struct {
	Type                   string       `json:"type"`
	Records                []HostRecord `json:"records"`
	Total                  int          `json:"total"`
	SentAnyHostMetricsData bool         `json:"sentAnyHostMetricsData"`
	IsSendingK8SAgentMetrics bool       `json:"isSendingK8SAgentMetrics"`
}

// HostListResponse is the response from /api/v1/hosts/list
type HostListResponse struct {
	Status string       `json:"status"`
	Data   HostListData `json:"data"`
}
