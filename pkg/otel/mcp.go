package otel

const UnknownMCPMethod = "unknown"

var knownMCPMethods = map[string]struct{}{
	"initialize":                           {},
	"ping":                                 {},
	"resources/list":                       {},
	"resources/templates/list":             {},
	"resources/read":                       {},
	"resources/subscribe":                  {},
	"resources/unsubscribe":                {},
	"prompts/list":                         {},
	"prompts/get":                          {},
	"tools/list":                           {},
	"tools/call":                           {},
	"logging/setLevel":                     {},
	"elicitation/create":                   {},
	"notifications/elicitation/complete":   {},
	"roots/list":                           {},
	"tasks/get":                            {},
	"tasks/list":                           {},
	"tasks/result":                         {},
	"tasks/cancel":                         {},
	"notifications/initialized":            {},
	"notifications/cancelled":              {},
	"notifications/progress":               {},
	"notifications/message":                {},
	"notifications/resources/list_changed": {},
	"notifications/resources/updated":      {},
	"notifications/prompts/list_changed":   {},
	"notifications/tools/list_changed":     {},
	"notifications/roots/list_changed":     {},
	"notifications/tasks/status":           {},
	"completion/complete":                  {},
	"sampling/createMessage":               {},
	"server/discover":                      {},
}

// NormalizeMCPMethod bounds client-controlled method names to a stable set of
// telemetry dimensions supported by the server.
func NormalizeMCPMethod(method string) string {
	if _, ok := knownMCPMethods[method]; ok {
		return method
	}
	return UnknownMCPMethod
}
