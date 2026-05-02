package tools

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed dashboard_templates.json
var dashboardTemplateCatalogJSON []byte

// dashboardTemplateEntry mirrors one entry in the bundled catalog.
type dashboardTemplateEntry struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Keywords    []string `json:"keywords"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
}

var dashboardTemplateCatalog []dashboardTemplateEntry

func init() {
	if err := json.Unmarshal(dashboardTemplateCatalogJSON, &dashboardTemplateCatalog); err != nil {
		// The catalog is committed alongside this code; failure here means the
		// embedded JSON is malformed. Fall back to empty so the tool still loads.
		dashboardTemplateCatalog = nil
	}
}

// listDashboardTemplates returns the embedded catalog. When category is
// non-empty, results are restricted to that catalog category (case-insensitive).
func listDashboardTemplates(category string) []dashboardTemplateEntry {
	if category == "" {
		out := make([]dashboardTemplateEntry, len(dashboardTemplateCatalog))
		copy(out, dashboardTemplateCatalog)
		return out
	}
	want := strings.ToLower(category)
	out := make([]dashboardTemplateEntry, 0, len(dashboardTemplateCatalog))
	for _, e := range dashboardTemplateCatalog {
		if strings.ToLower(e.Category) == want {
			out = append(out, e)
		}
	}
	return out
}
