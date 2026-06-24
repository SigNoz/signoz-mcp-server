package tools

import (
	"errors"
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

// requireArgsMap normalizes the raw MCP arguments payload into a JSON object map.
//
// A nil payload is NOT an error: the framework delivers an untyped nil when a
// tool is called with no "arguments" object at all, which is the common case of
// an omitted required parameter (or a legitimate no-args call to an all-optional
// tool). Treating nil as an EMPTY map lets the downstream per-field checks own
// the diagnosis — requireStringArg then emits the specific, actionable
// `"<field>" cannot be empty` (e.g. naming the missing ruleId/id), and a tool
// whose params are all optional simply proceeds with its defaults. Mapping nil
// to the generic "expected JSON object" guard instead would both mis-describe an
// omitted-arg call as malformed JSON and wrongly reject valid no-args list calls.
//
// A genuinely malformed payload — a non-nil value that is NOT an object (a JSON
// array, string, or scalar) — still returns the shared JSON-object guard, since
// no per-field check can run against it.
func requireArgsMap(raw any) (map[string]any, *mcp.CallToolResult) {
	if raw == nil {
		return map[string]any{}, nil
	}
	args, ok := raw.(map[string]any)
	if !ok {
		return nil, notAJSONObjectError()
	}
	if args == nil {
		// A typed nil map (e.g. JSON "arguments": null decoded into a
		// map[string]any) is not the untyped-nil case above; normalize it to a
		// non-nil empty map so the helper's success contract is uniform and
		// callers never have to special-case a nil map.
		return map[string]any{}, nil
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

// upstreamFetchError tags an error as originating from an upstream SigNoz client
// call inside a helper that otherwise returns a plain error mixing upstream and
// local-validation failures (e.g. resolveFormulaSubQuery, whose metadata
// auto-fetch hits the backend but whose "metric not found"/"validation error"
// paths are local). Wrapping only the upstream path lets the caller route it
// through upstreamError() for the uniform prefix while leaving the local
// validation errors raw.
type upstreamFetchError struct{ err error }

func (e *upstreamFetchError) Error() string { return e.err.Error() }
func (e *upstreamFetchError) Unwrap() error { return e.err }

// markUpstream tags err as an upstream-client failure so a caller can detect it
// via asUpstreamResult. Returns nil when err is nil.
func markUpstream(err error) error {
	if err == nil {
		return nil
	}
	return &upstreamFetchError{err: err}
}

// asUpstreamResult returns a uniform upstreamError result (and true) when err's
// chain contains an upstreamFetchError; otherwise (nil, false) so the caller can
// surface the error as a plain/local validation message.
func asUpstreamResult(err error) (*mcp.CallToolResult, bool) {
	var ufe *upstreamFetchError
	if errors.As(err, &ufe) {
		return upstreamError(err), true
	}
	return nil, false
}
