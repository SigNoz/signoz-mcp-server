package gen

import (
	"strings"
	"unicode"
)

// snakeCase converts CamelCase or camelCase to snake_case.
// "GetChannelByID" -> "get_channel_by_id"
// "listMetrics"    -> "list_metrics"
// Consecutive upper-case runs are treated as acronyms and emitted as one
// token: "URLPath" -> "url_path", not "u_r_l_path".
func snakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			// lowercase->upper: add underscore (getChannel -> get_channel).
			// upper->upper followed by lower (ID_Foo vs IDFoo): "IDFoo" =>
			// "id_foo" — underscore between the last upper of the acronym and
			// the next capitalized word.
			if unicode.IsLower(prev) ||
				(unicode.IsUpper(prev) && unicode.IsLower(next)) {
				b.WriteRune('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// goIdent converts an OpenAPI parameter name (which may contain hyphens, dots,
// or reserved words) into a valid exported Go identifier.
func goIdent(s string) string {
	if s == "" {
		return "Unnamed"
	}
	var b strings.Builder
	upperNext := true
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			upperNext = true
			continue
		}
		if upperNext {
			b.WriteRune(unicode.ToUpper(r))
			upperNext = false
		} else {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || !unicode.IsLetter(rune(out[0])) {
		out = "F" + out
	}
	return out
}

// toolName returns the MCP tool name for an operation: "signoz_" + snake_case(operationId).
func toolName(operationID string) string {
	return "signoz_" + snakeCase(operationID)
}
