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

// searchDashboardTemplates returns the catalog entries whose text fields
// contain every whitespace-separated word of searchText, case-insensitively.
// An empty searchText returns the whole catalog.
func searchDashboardTemplates(searchText string) []dashboardTemplateEntry {
	words := strings.Fields(strings.ToLower(searchText))
	out := make([]dashboardTemplateEntry, 0, len(dashboardTemplateCatalog))
	for _, entry := range dashboardTemplateCatalog {
		// Words contain no spaces, so a match cannot straddle two fields.
		text := strings.ToLower(strings.Join(append([]string{entry.ID, entry.Title, entry.Path, entry.Category, entry.Description}, entry.Keywords...), " "))
		if containsAllWords(text, words) {
			out = append(out, entry)
		}
	}
	return out
}

func containsAllWords(text string, words []string) bool {
	for _, word := range words {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}
