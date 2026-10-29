package types

import (
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// TestAIAttributeMappingInput is the signoz_test_ai_attribute_mapping input
// schema: the generated span mapper test request fields plus searchContext.
// groups is optional here because the handler fills in the saved groups.
type TestAIAttributeMappingInput struct {
	Spans         []apitypes.SpantypesSpanMapperTestSpan          `json:"spans" jsonschema:"1 to 100 sample spans. Each span has attributes and resource maps of key to value; copy them from a real span or from what the framework emits. Example: [{\"attributes\":{\"llm.model_name\":\"gpt-4o\",\"llm.token_count.prompt\":120,\"llm.token_count.completion\":45},\"resource\":{\"service.name\":\"chat-api\"}}]."`
	Groups        []apitypes.SpantypesPostableSpanMapperTestGroup `json:"groups,omitempty" jsonschema:"Mapping groups to test instead of the saved ones, such as a draft before adding it on the Attribute Mapping tab. Omit to test the saved groups; an empty list tests none. A group without mappers uses the saved mappers of the group with the same name. SigNoz skips disabled groups, mappers, and condition keys, so set enabled to true on each."`
	SearchContext string                                          `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}
