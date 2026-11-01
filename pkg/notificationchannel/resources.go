// Package notificationchannel holds the MCP resource content served at
// signoz://notification-channel/examples: the provider kinds SigNoz accepts,
// every config.spec field per kind, and the request examples the SigNoz
// OpenAPI spec publishes, all rendered by tools/specgen/specgen.py.
package notificationchannel

import _ "embed"

// generatedExamples is rendered by tools/specgen/specgen.py from the pinned
// OpenAPI spec: the per-kind config.spec field reference and the request
// examples of the notification-channel operations.
//
//go:embed examples.generated.md
var generatedExamples string

// Examples is the body of signoz://notification-channel/examples.
var Examples = examplesPreamble + generatedExamples

const examplesPreamble = `# Notification Channel Reference and Examples

The create examples are signoz_create_notification_channel arguments. The replace examples are full replacement bodies: pass their "config" object as the config argument of signoz_update_notification_channel, with "id" beside it. Webhook URLs, tokens, and keys are placeholders: use the values the user provides, never invent them. name is an immutable DNS-1123 label and alert routing references displayName, so pick a displayName the user will recognize in alert rules.

`
