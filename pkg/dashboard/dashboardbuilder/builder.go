// Package dashboardbuilder provides validation, transformation, and a fluent builder
// API for constructing SigNoz dashboard payloads. It is designed to be imported by
// external repositories (e.g., an MCP server) that programmatically create dashboards
// via the SigNoz API.
//
// Dashboards default to private — public dashboard fields are deliberately excluded.
// Variables default to DYNAMIC type when no type is specified.
//
// Usage:
//
//	dashboard, err := dashboardbuilder.New("Service Latency").
//	    Description("P99 latency monitoring").
//	    Tags("latency", "sre").
//	    AddVariable("service", dashboardbuilder.NewDynamicVariable("service.name", "Traces")).
//	    AddTimeSeriesWidget("P99 Latency", dashboardbuilder.NewBuilderQuery(
//	        map[string]interface{}{
//	            "queryName":  "A",
//	            "dataSource": "traces",
//	            "aggregateOperator": "p99",
//	            "expression": "A",
//	        },
//	    )).
//	    BuildMap()
package dashboardbuilder

import "github.com/google/uuid"

// DashboardBuilder provides a fluent API for constructing valid dashboard payloads.
type DashboardBuilder struct {
	data *DashboardData
}

// New creates a new DashboardBuilder with the given title.
func New(title string) *DashboardBuilder {
	return &DashboardBuilder{
		data: &DashboardData{
			Title:     title,
			Variables: make(map[string]*DashboardVariable),
		},
	}
}

// Description sets the dashboard description.
func (b *DashboardBuilder) Description(desc string) *DashboardBuilder {
	b.data.Description = desc
	return b
}

// Tags sets the dashboard tags.
func (b *DashboardBuilder) Tags(tags ...string) *DashboardBuilder {
	b.data.Tags = tags
	return b
}

// AddVariable adds a dashboard variable. The key is the variable identifier
// used in queries (e.g., "$service").
func (b *DashboardBuilder) AddVariable(key string, v *DashboardVariable) *DashboardBuilder {
	b.data.Variables[key] = v
	return b
}

// AddWidget adds a widget or row to the dashboard.
func (b *DashboardBuilder) AddWidget(w WidgetOrRow) *DashboardBuilder {
	b.data.Widgets = append(b.data.Widgets, w)
	return b
}

// AddRow adds a row separator with the given title.
func (b *DashboardBuilder) AddRow(title string) *DashboardBuilder {
	b.data.Widgets = append(b.data.Widgets, WidgetOrRow{
		ID:         uuid.NewString(),
		PanelTypes: PanelTypeRow,
		Title:      title,
	})
	return b
}

// AddTimeSeriesWidget is a convenience method to add a "graph" panel widget.
func (b *DashboardBuilder) AddTimeSeriesWidget(title string, query Query) *DashboardBuilder {
	return b.AddWidget(WidgetOrRow{
		PanelTypes: PanelTypeGraph,
		Title:      title,
		Query:      &query,
	})
}

// AddValueWidget is a convenience method to add a "value" panel widget.
func (b *DashboardBuilder) AddValueWidget(title string, query Query) *DashboardBuilder {
	return b.AddWidget(WidgetOrRow{
		PanelTypes: PanelTypeValue,
		Title:      title,
		Query:      &query,
	})
}

// AddTableWidget is a convenience method to add a "table" panel widget.
func (b *DashboardBuilder) AddTableWidget(title string, query Query) *DashboardBuilder {
	return b.AddWidget(WidgetOrRow{
		PanelTypes: PanelTypeTable,
		Title:      title,
		Query:      &query,
	})
}

// AddBarWidget is a convenience method to add a "bar" panel widget.
func (b *DashboardBuilder) AddBarWidget(title string, query Query) *DashboardBuilder {
	return b.AddWidget(WidgetOrRow{
		PanelTypes: PanelTypeBar,
		Title:      title,
		Query:      &query,
	})
}

// AddPieWidget is a convenience method to add a "pie" panel widget.
func (b *DashboardBuilder) AddPieWidget(title string, query Query) *DashboardBuilder {
	return b.AddWidget(WidgetOrRow{
		PanelTypes: PanelTypePie,
		Title:      title,
		Query:      &query,
	})
}

// AddListWidget is a convenience method to add a "list" panel widget.
func (b *DashboardBuilder) AddListWidget(title string, query Query) *DashboardBuilder {
	return b.AddWidget(WidgetOrRow{
		PanelTypes: PanelTypeList,
		Title:      title,
		Query:      &query,
	})
}

// Build applies defaults, validates, and returns the DashboardData.
func (b *DashboardBuilder) Build() (*DashboardData, error) {
	applyBuilderDefaults(b.data)
	if verr := Validate(b.data); verr != nil {
		return nil, verr
	}
	return b.data, nil
}

// BuildMap is Build + ToMap for direct use with the SigNoz API.
func (b *DashboardBuilder) BuildMap() (map[string]interface{}, error) {
	d, err := b.Build()
	if err != nil {
		return nil, err
	}
	return d.ToMap()
}

// --- Variable constructors ---

// NewDynamicVariable creates a DYNAMIC type variable.
// attribute is the field to query (e.g., "service.name").
// source is one of "Traces", "Logs", "Metrics", or "all sources".
func NewDynamicVariable(attribute, source string) *DashboardVariable {
	return &DashboardVariable{
		Type:                      VariableTypeDynamic,
		DynamicVariablesAttribute: attribute,
		DynamicVariablesSource:    source,
		MultiSelect:               true,
		ShowALLOption:             true,
	}
}

// NewQueryVariable creates a QUERY type variable with the given SQL query.
func NewQueryVariable(queryValue string) *DashboardVariable {
	return &DashboardVariable{
		Type:       VariableTypeQuery,
		QueryValue: queryValue,
	}
}

// NewCustomVariable creates a CUSTOM type variable with comma-separated values.
func NewCustomVariable(customValue string) *DashboardVariable {
	return &DashboardVariable{
		Type:        VariableTypeCustom,
		CustomValue: customValue,
	}
}

// NewTextboxVariable creates a TEXTBOX type variable with a default value.
func NewTextboxVariable(defaultValue string) *DashboardVariable {
	return &DashboardVariable{
		Type:         VariableTypeTextbox,
		TextboxValue: defaultValue,
		DefaultValue: defaultValue,
	}
}

// --- Query constructors ---

// NewBuilderQuery creates a builder-type Query from one or more query data entries.
func NewBuilderQuery(queryData ...map[string]interface{}) Query {
	return Query{
		QueryType: QueryTypeBuilder,
		Builder: &BuilderData{
			QueryData:     queryData,
			QueryFormulas: []map[string]interface{}{},
		},
		ClickhouseSQL: []ClickHouseQuery{},
		PromQL:        []PromQLQuery{},
	}
}

// NewPromQLQuery creates a PromQL-type Query.
func NewPromQLQuery(queries ...PromQLQuery) Query {
	return Query{
		QueryType:     QueryTypePromQL,
		PromQL:        queries,
		ClickhouseSQL: []ClickHouseQuery{},
	}
}

// NewClickHouseQuery creates a ClickHouse SQL-type Query.
func NewClickHouseQuery(queries ...ClickHouseQuery) Query {
	return Query{
		QueryType:     QueryTypeClickHouse,
		ClickhouseSQL: queries,
		PromQL:        []PromQLQuery{},
	}
}
