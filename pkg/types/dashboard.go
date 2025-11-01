package types

type Dashboard struct {
	Title       string   `json:"title" jsonschema:"required" jsonschema_extras:"description=The display name of the dashboard."`
	Description string   `json:"description,omitempty" jsonschema_extras:"description=A brief explanation of what the dashboard shows."`
	Tags        []string `json:"tags,omitempty" jsonschema_extras:"description=Keywords for categorization e.g performance latency."`

	Layout    []LayoutItem        `json:"layout" jsonschema:"required" jsonschema_extras:"description=Defines the grid positioning and size for each widget."`
	Variables map[string]Variable `json:"variables,omitempty" jsonschema_extras:"description=Key-value map of template variables available for queries."`

	Widgets []Widget `json:"widgets" jsonschema:"required" jsonschema_extras:"description=The list of all graphical components displayed on the dashboard."`
}

type LayoutItem struct {
	X      int    `json:"x" jsonschema:"required" jsonschema_extras:"description=The x-coordinate (column) of the item."`
	Y      int    `json:"y" jsonschema:"required" jsonschema_extras:"description=The y-coordinate (row) of the item."`
	W      int    `json:"w" jsonschema:"required" jsonschema_extras:"description=The width of the item in grid units."`
	H      int    `json:"h" jsonschema:"required" jsonschema_extras:"description=The height of the item in grid units."`
	I      string `json:"i" jsonschema:"required" jsonschema_extras:"description=The unique ID linking the layout item to a specific widget."`
	Moved  bool   `json:"moved,omitempty" jsonschema_extras:"description=Indicates if the item has been moved from its default position."`
	Static bool   `json:"static,omitempty" jsonschema_extras:"description=If true, the item cannot be moved or resized."`
}

type Variable struct {
	ID            string `json:"id,omitempty" jsonschema_extras:"description=The unique ID of the variable."`
	Name          string `json:"name,omitempty" jsonschema_extras:"description=The user-facing display name."`
	Description   string `json:"description,omitempty" jsonschema_extras:"description=Description of the variable's purpose."`
	Key           string `json:"key,omitempty" jsonschema_extras:"description=The internal key used in queries e.g $instance."`
	Type          string `json:"type,omitempty" jsonschema_extras:"description=The variable type e.g query or textbox."`
	QueryValue    string `json:"queryValue,omitempty" jsonschema_extras:"description=The expression used to populate variable options."`
	AllSelected   bool   `json:"allSelected,omitempty" jsonschema_extras:"description=True if the All option is selected."`
	CustomValue   string `json:"customValue,omitempty" jsonschema_extras:"description=Custom user-defined value if supported."`
	MultiSelect   bool   `json:"multiSelect,omitempty" jsonschema_extras:"description=Allows selecting multiple values."`
	Order         int    `json:"order,omitempty" jsonschema_extras:"description=Display order in the UI."`
	ShowALLOption bool   `json:"showALLOption,omitempty" jsonschema_extras:"description=If true the All option appears in the list."`
	Sort          string `json:"sort,omitempty" jsonschema_extras:"description=Sorting of variable options."`
	TextboxValue  string `json:"textboxValue,omitempty" jsonschema_extras:"description=Current value for a textbox variable."`
}

type Widget struct {
	ID             string    `json:"id" jsonschema:"required" jsonschema_extras:"description=Unique identifier for the widget."`
	Description    string    `json:"description,omitempty" jsonschema_extras:"description=Details about the data shown in the panel."`
	IsStacked      bool      `json:"isStacked,omitempty" jsonschema_extras:"description=Applies to graph panels. True means stacked series."`
	NullZeroValues bool      `json:"nullZeroValues,omitempty" jsonschema_extras:"description=Treats null values as zero."`
	Opacity        int       `json:"opacity,omitempty" jsonschema_extras:"description=Transparency level of the visualization."`
	PanelTypes     string    `json:"panelTypes" jsonschema:"required" jsonschema_extras:"description=Visualization type e.g - graph table value list trace."`
	TimePreferance string    `json:"timePreferance,omitempty" jsonschema_extras:"description=Widget-specific time override instead of dashboard time."`
	Title          string    `json:"title" jsonschema:"required" jsonschema_extras:"description=Title displayed at the top of the widget."`
	YAxisUnit      string    `json:"yAxisUnit,omitempty" jsonschema_extras:"description=Unit of the Y-axis e.g. ms | percent"`
	Query          QueryBody `json:"query" jsonschema:"required" jsonschema_extras:"description=Data source and expressions used to fetch data for the widget."`
}

type QueryBody struct {
	PromQL        []string               `json:"promql,omitempty" jsonschema_extras:"description=List of Prometheus Query Language expressions."`
	ClickhouseSQL []string               `json:"clickhouse_sql,omitempty" jsonschema_extras:"description=List of ClickHouse SQL queries."`
	Builder       map[string]interface{} `json:"builder,omitempty" jsonschema_extras:"description=Configuration for visual query builder mode."`
}
