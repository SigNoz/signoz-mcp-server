package types

// QueryType identifies how a query is expressed. It is shared by alert rules
// (and historically by dashboard widgets). Allowed values mirror the SigNoz
// frontend EQueryType enum.
type QueryType string

const (
	QueryTypeBuilder       QueryType = "builder"
	QueryTypeClickHouseSQL QueryType = "clickhouse_sql"
	QueryTypePromQL        QueryType = "promql"
)
