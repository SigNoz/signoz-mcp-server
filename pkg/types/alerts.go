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
