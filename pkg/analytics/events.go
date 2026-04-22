package analytics

// Events follow "MCP <Subsection>: <Action>". The "MCP" prefix keeps these
// distinct from main-app SigNoz events in shared destinations. IDs go in
// attributes, never in the event name — cardinality stays bounded.
const (
	EventSessionRegistered           = "MCP Session: Registered"
	EventSessionUnregistered         = "MCP Session: Unregistered"
	EventToolCalled                  = "MCP Tool: Called"
	EventPromptFetched               = "MCP Prompt: Fetched"
	EventResourceFetched             = "MCP Resource: Fetched"
	EventOAuthAuthorizationSubmitted = "MCP OAuth: Authorization submitted"
	EventOAuthTokenIssued            = "MCP OAuth: Token issued"
)

// camelCase for analytics destinations. OTel span attrs use the dotted
// semantic-convention form and live in pkg/otel.
const (
	AttrOrgID           = "orgId"
	AttrPrincipal       = "principal"
	AttrName            = "name"
	AttrEmail           = "email"
	AttrTenantURL       = "tenantUrl"
	AttrSessionID       = "sessionId"
	AttrClientName      = "clientName"
	AttrClientVersion   = "clientVersion"
	AttrToolName        = "toolName"
	AttrToolIsError     = "toolIsError"
	AttrDurationMs      = "durationMs"
	AttrSearchContext   = "searchContext"
	AttrPromptName      = "promptName"
	AttrResourceURI     = "resourceUri"
	AttrGrantType       = "grantType"
	AttrProtocolVersion = "protocolVersion"
	AttrErrorType       = "errorType"
)
