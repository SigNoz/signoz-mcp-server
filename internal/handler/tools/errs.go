package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mark3labs/mcp-go/mcp"
)

// Shared error/validation string helpers used across the MCP tool handlers.
// Converging on these keeps the strings uniform so the AI assistant can tell a
// user/parameter mistake (fix and re-call) apart from an upstream SigNoz
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
	// validationErrorPrefix is the canonical prefix for all parameter/validation failures.
	validationErrorPrefix = "Parameter validation failed:"

	// upstreamErrorPrefix marks failures originating from the SigNoz backend
	// (any client API call), giving the LLM a detectable signal to distinguish
	// them from a local parameter mistake.
	upstreamErrorPrefix = "SigNoz API error:"

	// notAJSONObjectMessage guards read-only tools whose arguments payload is not a JSON object.
	notAJSONObjectMessage = "invalid arguments format: expected JSON object"

	// notAConfigObjectMessage is the body-carrying-tool variant (create/update tools whose payload is the resource body).
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
	//
	// RESERVED for now: the backend signals "not found" only via prose in the
	// error body, and distinguishing it from other upstream failures would
	// require brittle string-matching, which #365 deliberately avoids. Upstream
	// 404s currently surface as UPSTREAM_ERROR. notFoundError + this code are
	// kept as the stable home for a future, robust not-found signal (e.g. a
	// typed status code from the client layer).
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

// validationError builds a canonical result of the form:
// Parameter validation failed: "<field>" <reason>
// reason is a clause that follows the quoted field, e.g. "must be a string".
func validationError(field, reason string) *mcp.CallToolResult {
	return errorWithCode(CodeValidationFailed, fmt.Sprintf(`%s %q %s`, validationErrorPrefix, field, reason))
}

// validationErrorf is validationError with a printf-style reason, for call
// sites that interpolate values into the reason.
func validationErrorf(field, reasonFormat string, args ...any) *mcp.CallToolResult {
	return validationError(field, fmt.Sprintf(reasonFormat, args...))
}

// requireStringArg reads a required string argument, returning a two-tier
// canonical error: "must be a string" for a wrong-typed value, "cannot be
// empty" for a missing or empty one (so wrong-type and absence are not conflated).
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

// notAJSONObjectError is the shared guard result for a non-object arguments payload.
func notAJSONObjectError() *mcp.CallToolResult {
	return errorWithCode(CodeValidationFailed, notAJSONObjectMessage)
}

// requireArgsMap normalizes the raw MCP arguments payload into a JSON object map.
// A nil payload is treated as an empty map (the common omitted-args / no-args
// call) so the per-field checks own the diagnosis — emitting a specific
// `"<field>" cannot be empty` rather than mislabeling it as malformed JSON and
// rejecting valid no-args list calls. A non-nil, non-object payload (array,
// string, scalar) returns the shared JSON-object guard.
func requireArgsMap(raw any) (map[string]any, *mcp.CallToolResult) {
	if raw == nil {
		return map[string]any{}, nil
	}
	args, ok := raw.(map[string]any)
	if !ok {
		return nil, notAJSONObjectError()
	}
	if args == nil {
		// Typed-nil map (JSON "arguments": null): normalize to a non-nil empty map.
		return map[string]any{}, nil
	}
	return args, nil
}

// requireStringField is the error-returning sibling of requireStringArg, for
// helpers that propagate a plain error (e.g. notification-channel receiver
// builders). Same two-tier rule; requiredReason is appended after the
// "is required" clause to carry per-field guidance.
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

// notAConfigObjectError is the body-carrying-tool variant of the arguments guard.
func notAConfigObjectError() *mcp.CallToolResult {
	return errorWithCode(CodeValidationFailed, notAConfigObjectMessage)
}

// upstreamError wraps a SigNoz backend client error in the uniform upstream
// prefix, so the LLM can distinguish a backend problem (retry) from a parameter
// problem (fix).
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
// SCOPE NOTE: several mutation results deliberately stay text-only and do NOT
// route through this helper — the create/update passthrough bodies
// (create_alert in alerts.go, create_dashboard in dashboards.go, create_view /
// update_view in views.go) and the plain-text update/delete dashboard
// acknowledgements ("dashboard updated" / "dashboard deleted" in dashboards.go).
// Standardizing those mutation success envelopes is OUT OF SCOPE for #365 and
// tracked at SigNoz/nerve-pod#4; here we only added structuredContent to
// mutations that ALREADY return synthesized JSON.
//
// jsonPayload must be the exact bytes also placed in the text block so the two
// representations never diverge. It is decoded into a generic value for the
// StructuredContent field using a json.Number-mode decoder so large SigNoz
// integers (epoch nanos, big ids/counts > 2^53) are preserved EXACTLY. A plain
// json.Unmarshal into `any` routes every number through float64, which silently
// rounds values above 2^53 and would make StructuredContent disagree with the
// byte-faithful text block.
func structuredResult(jsonPayload []byte) *mcp.CallToolResult {
	dec := json.NewDecoder(bytes.NewReader(jsonPayload))
	dec.UseNumber()
	var structured any
	if err := dec.Decode(&structured); err != nil {
		// Fail open: if the payload isn't valid JSON (should not happen for a
		// code-controlled tool), fall back to a plain text result so the caller
		// still gets the data rather than an error.
		return mcp.NewToolResultText(string(jsonPayload))
	}
	// The text block carries the WHOLE payload, so StructuredContent must
	// represent the whole payload too — exactly one JSON value with nothing
	// after it. If there is trailing junk or a second value, the decoded
	// `structured` only covers the first value and would silently disagree with
	// block 0; fail open to text-only rather than advertise a divergent shape.
	if err := dec.Decode(new(any)); err != io.EOF {
		return mcp.NewToolResultText(string(jsonPayload))
	}
	return mcp.NewToolResultStructured(structured, string(jsonPayload))
}

// upstreamFetchError tags an error as upstream-originated inside a helper that
// otherwise mixes upstream and local-validation failures (e.g.
// resolveFormulaSubQuery), so the caller can route only the upstream path
// through upstreamError() and leave the local validation errors raw.
type upstreamFetchError struct{ err error }

func (e *upstreamFetchError) Error() string { return e.err.Error() }
func (e *upstreamFetchError) Unwrap() error { return e.err }

// markUpstream tags err as an upstream-client failure (detectable via
// asUpstreamResult). Returns nil when err is nil.
func markUpstream(err error) error {
	if err == nil {
		return nil
	}
	return &upstreamFetchError{err: err}
}

// asUpstreamResult returns a uniform upstreamError result (and true) when err's
// chain contains an upstreamFetchError; otherwise (nil, false).
func asUpstreamResult(err error) (*mcp.CallToolResult, bool) {
	var ufe *upstreamFetchError
	if errors.As(err, &ufe) {
		return upstreamError(err), true
	}
	return nil, false
}
