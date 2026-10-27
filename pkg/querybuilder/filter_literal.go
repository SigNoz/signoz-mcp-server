package querybuilder

import "strings"

// QuoteFilterLiteral escapes a value for a single-quoted Query Builder string:
// each backslash becomes two backslashes first, then each apostrophe is
// backslash-escaped, matching the backend's FilterStringLiteral rules.
func QuoteFilterLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}
