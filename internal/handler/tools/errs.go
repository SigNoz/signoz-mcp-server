package tools

import (
	"encoding/json"
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
// Family C (#365) layers a light, machine-readable `code` taxonomy on top of
// these helpers, surfaced via the result's StructuredContent ({"code": ...}).
// The human-readable text block is left unchanged — the code is purely additive
// so an MCP client/LLM can branch on a stable token (e.g. retry an
// UPSTREAM_ERROR vs fix args on a VALIDATION_FAILED) instead of string-matching
// the prose. This mirrors the docs tools' existing {code,message} error pattern
// (internal/docs/errors.go). The code is derived from the helper that produced
// the result, so call sites need no changes.

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

// Error-code taxonomy surfaced in StructuredContent on error results. Keep this
// set small and stable — it is a contract MCP clients branch on. Values mirror
// the docs tools' {code,...} pattern (internal/docs/errors.go).
const (
	// CodeValidationFailed marks a fixable parameter/validation mistake: the
	// caller should correct the arguments and retry. Emitted by
	// validationError/validationErrorf and the arguments-guard helpers.
	CodeValidationFailed = "VALIDATION_FAILED"

	// CodeUpstreamError marks a SigNoz backend failure: typically retryable
	// without changing arguments. Emitted by upstreamError.
	CodeUpstreamError = "UPSTREAM_ERROR"

	// CodeNotFound marks a referenced resource that does not exist (e.g. a bad
	// id/uuid). Callers should not blindly retry — re-discover the id first.
	CodeNotFound = "NOT_FOUND"
)

// errorWithCode builds an error result whose text block is message (unchanged,
// human-readable) and whose StructuredContent carries {"code": code} so clients
// can branch on a stable token. This is the single shaping point for all
// coded error results.
func errorWithCode(code, message string) *mcp.CallToolResult {
	res := mcp.NewToolResultError(message)
	res.StructuredContent = map[string]any{"code": code}
	return res
}

// validationError builds a canonical parameter-validation error result of the
// form: Parameter validation failed: "<field>" <reason>
//
// reason should read as a clause that follows the quoted field name, e.g.
// "must be a string" or "is required. Use signoz_list_metrics to find metrics".
func validationError(field, reason string) *mcp.CallToolResult {
	return errorWithCode(CodeValidationFailed, fmt.Sprintf(`%s %q %s`, validationErrorPrefix, field, reason))
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
	return errorWithCode(CodeValidationFailed, notAJSONObjectMessage)
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
	return errorWithCode(CodeValidationFailed, notAConfigObjectMessage)
}

// upstreamError wraps an error returned by a SigNoz backend client call in the
// uniform upstream prefix. Use this for every upstream API failure so the LLM
// can distinguish a backend problem (retry) from a parameter problem (fix).
func upstreamError(err error) *mcp.CallToolResult {
	return errorWithCode(CodeUpstreamError, fmt.Sprintf("%s %s", upstreamErrorPrefix, err.Error()))
}

// notFoundError marks a referenced resource that does not exist. The message is
// the human-readable explanation; the NOT_FOUND code lets clients avoid a blind
// retry and re-discover the id instead.
func notFoundError(message string) *mcp.CallToolResult {
	return errorWithCode(CodeNotFound, message)
}

// structuredResult is the shared success-path wrapper for tools whose output
// JSON shape is CODE-CONTROLLED — i.e. this server builds the JSON envelope, so
// an outputSchema/structuredContent contract is stable and worth advertising.
//
// Two-tier rule (Family C #365):
//   - Code-controlled tools (paginate.Wrap list/summary tools, single-resource
//     get_*, and mutation results that return synthesized JSON) carry the same
//     JSON in BOTH the text block (block 0, for back-compat) and
//     StructuredContent, via this helper.
//   - Raw QB passthrough tools (search_logs/search_traces/aggregate_logs/
//     aggregate_traces/query_metrics) return the backend's JSON verbatim. Its
//     shape is variable/upstream-owned, so an outputSchema there would be
//     brittle and drift out from under us — those stay text-only (see
//     resultWithNotes / rawSearchResult / aggregateResult).
//
// jsonPayload must be the exact bytes also placed in the text block so the two
// representations never diverge. It is re-decoded into a generic value for the
// StructuredContent field (MCP serializes that separately).
func structuredResult(jsonPayload []byte) *mcp.CallToolResult {
	var structured any
	if err := json.Unmarshal(jsonPayload, &structured); err != nil {
		// Fail open: if the payload isn't valid JSON (should not happen for a
		// code-controlled tool), fall back to a plain text result so the caller
		// still gets the data rather than an error.
		return mcp.NewToolResultText(string(jsonPayload))
	}
	return mcp.NewToolResultStructured(structured, string(jsonPayload))
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
