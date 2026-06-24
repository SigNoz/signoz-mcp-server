package tools

import (
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// Shared error/validation string helpers used across the MCP tool handlers.
// Converging on these keeps the strings uniform so the AI assistant can tell a
// user/parameter mistake (fix and re-call) apart from an upstream SigNoz
// failure (retryable).

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

// validationError builds a canonical result of the form:
// Parameter validation failed: "<field>" <reason>
// reason is a clause that follows the quoted field, e.g. "must be a string".
func validationError(field, reason string) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(`%s %q %s`, validationErrorPrefix, field, reason))
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
	return mcp.NewToolResultError(notAJSONObjectMessage)
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
	return mcp.NewToolResultError(notAConfigObjectMessage)
}

// upstreamError wraps a SigNoz backend client error in the uniform upstream
// prefix, so the LLM can distinguish a backend problem (retry) from a parameter
// problem (fix).
func upstreamError(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("%s %s", upstreamErrorPrefix, err.Error()))
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
