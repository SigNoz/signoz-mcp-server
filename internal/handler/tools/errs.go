package tools

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// This file holds the shared error/validation string helpers used across all
// MCP tool handlers. Converging on these helpers keeps the strings the AI
// assistant sees uniform, so it can reliably tell a user/parameter mistake
// (fixable by re-calling with corrected args) apart from an upstream SigNoz
// failure (retryable).
//
// Design note for Family C (#365): these helpers are intentionally shaped so a
// structured `code` taxonomy (e.g. VALIDATION_FAILED, UPSTREAM_ERROR) can be
// layered on later without changing call sites — the code would be derived from
// the helper that produced the result. Do NOT add codes here yet.

const (
	// validationErrorPrefix is the canonical capital-P prefix for all
	// parameter/validation failures. The historical lowercase "parameter
	// validation failed:" variants are converged onto this.
	validationErrorPrefix = "Parameter validation failed:"

	// upstreamErrorPrefix is the uniform prefix for every failure that
	// originates from the SigNoz backend (any client API call). It gives the
	// LLM a machine-detectable marker to distinguish an upstream failure from a
	// local parameter mistake.
	upstreamErrorPrefix = "SigNoz API error:"

	// notAJSONObjectMessage is the shared guard message for read-only tools
	// whose entire arguments payload failed to decode as a JSON object.
	notAJSONObjectMessage = "invalid arguments format: expected JSON object"

	// notAConfigObjectMessage is the body-carrying-tool variant of the
	// arguments guard, used by create/update tools whose payload IS the
	// resource body (alerts/dashboards/views).
	notAConfigObjectMessage = `Parameter validation failed: the configuration object is empty or improperly formatted.`
)

// validationError builds a canonical parameter-validation error result of the
// form: Parameter validation failed: "<field>" <reason>
//
// reason should read as a clause that follows the quoted field name, e.g.
// "must be a string" or "is required. Use signoz_list_metrics to find metrics".
func validationError(field, reason string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(`%s %q %s`, validationErrorPrefix, field, reason))
}

// validationErrorf is a convenience wrapper for validationError whose reason is
// built from a format string. It exists so call sites that interpolate values
// into the reason (e.g. an index or a "got %q") stay on the canonical prefix.
func validationErrorf(field, reasonFormat string, args ...any) *mcp.CallToolResult {
	return validationError(field, fmt.Sprintf(reasonFormat, args...))
}

// requireStringArg reads a required string argument from args. It returns the
// value and a nil result on success. On failure it returns "" and a two-tier
// canonical validation error:
//   - "must be a string" when the key is present but not a string (wrong type),
//   - "cannot be empty" when the key is missing or an empty string.
//
// This replaces the ad-hoc one- and two-tier id/name readers that previously
// mislabeled a wrong-typed value as "empty".
func requireStringArg(args map[string]any, key string) (string, *mcp.CallToolResult) {
	if args == nil {
		return "", validationError(key, "cannot be empty")
	}
	raw, present := args[key]
	if !present {
		return "", validationError(key, "cannot be empty")
	}
	s, ok := raw.(string)
	if !ok {
		return "", validationError(key, "must be a string")
	}
	if s == "" {
		return "", validationError(key, "cannot be empty")
	}
	return s, nil
}

// notAJSONObjectError is the shared guard result for read-only tools whose
// arguments payload is not a JSON object.
func notAJSONObjectError() *mcp.CallToolResult {
	return mcp.NewToolResultError(notAJSONObjectMessage)
}

// requireArgsMap asserts that the raw MCP arguments payload is a JSON object and
// returns it. On a non-object payload (including an untyped nil, which is what
// the framework delivers when a tool is called with no "arguments" at all) it
// returns the shared JSON-object guard result. Use this before requireStringArg
// so a non-object payload yields the JSON-object guard rather than a misleading
// "<field> cannot be empty".
func requireArgsMap(raw any) (map[string]any, *mcp.CallToolResult) {
	args, ok := raw.(map[string]any)
	if !ok {
		return nil, notAJSONObjectError()
	}
	return args, nil
}

// requireStringField is the error-returning sibling of requireStringArg, for
// helpers that propagate a plain error (e.g. notification-channel receiver
// builders) rather than a tool result. It applies the same two-tier rule —
// "must be a string" for a wrong-typed value, "is required" for a missing or
// empty one — so wrong-type and absence are not conflated. reason is appended
// after the "is required" clause to carry per-field guidance.
func requireStringField(args map[string]any, key, requiredReason string) (string, error) {
	if raw, present := args[key]; present {
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf(`%s %q must be a string`, validationErrorPrefix, key)
		}
		if s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf(`%s %q is required%s`, validationErrorPrefix, key, requiredReason)
}

// notAConfigObjectError is the body-carrying-tool variant of the arguments
// guard (create/update tools whose payload is the resource body).
func notAConfigObjectError() *mcp.CallToolResult {
	return mcp.NewToolResultError(notAConfigObjectMessage)
}

// upstreamError wraps an error returned by a SigNoz backend client call in the
// uniform upstream prefix. Use this for every upstream API failure so the LLM
// can distinguish a backend problem (retry) from a parameter problem (fix).
func upstreamError(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("%s %s", upstreamErrorPrefix, err.Error()))
}
