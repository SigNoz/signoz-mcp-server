package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
)

// Shared error/validation string helpers used across the MCP tool handlers.
// Converging on these keeps the strings uniform so the AI assistant can tell a
// user/parameter mistake (fix and re-call) apart from an upstream SigNoz
// failure (inspect status/code before retrying).
//
// Error results also carry a machine-readable `code` via StructuredContent
// (and sometimes extra fields like status), leaving the text block unchanged,
// so clients can branch on stable fields instead of string-matching prose.

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
	// caller should correct the arguments and retry. Emitted by local
	// validation helpers and upstream HTTP 400 responses.
	CodeValidationFailed = toolerrors.CodeValidationFailed

	// CodeUpstreamError marks a generic SigNoz backend failure. Emitted by
	// upstreamError when no more precise status-derived code applies.
	CodeUpstreamError = toolerrors.CodeUpstreamError

	// CodeUnauthorized marks a SigNoz backend 401. The caller should re-authenticate
	// or provide valid credentials rather than blindly retrying.
	CodeUnauthorized = toolerrors.CodeUnauthorized

	// CodePermissionDenied marks a SigNoz backend 403. The caller should ask for
	// permissions or use an account with the required role.
	CodePermissionDenied = toolerrors.CodePermissionDenied

	// CodeNotFound marks a missing resource (bad id/uuid) — re-discover the id
	// rather than blindly retry. Emitted by local guards and upstream HTTP 404s.
	CodeNotFound = toolerrors.CodeNotFound

	// CodeConflict marks an upstream HTTP 409.
	CodeConflict = toolerrors.CodeConflict

	// CodeRateLimited marks an upstream HTTP 429.
	CodeRateLimited = toolerrors.CodeRateLimited

	// CodeUnsupported marks an upstream HTTP 501.
	CodeUnsupported = toolerrors.CodeUnsupported

	// CodeLicenseUnavailable marks an upstream HTTP 451.
	CodeLicenseUnavailable = toolerrors.CodeLicenseUnavailable

	// CodeCanceled marks an upstream/client-closed HTTP 499.
	CodeCanceled = toolerrors.CodeCanceled

	// CodeTimeout marks an upstream HTTP timeout response.
	CodeTimeout = toolerrors.CodeTimeout
)

const statusClientClosedConnection = 499

var assistantAuthEnvelopeCodes = map[string]struct{}{
	"forbidden":       {},
	"token_expired":   {},
	"unauthenticated": {},
}

// errorWithCode builds an error result whose text block is message and whose
// StructuredContent carries {"code": code} — the single shaping point for all
// coded error results.
func errorWithCode(code, message string) *mcp.CallToolResult {
	return errorWithStructuredContent(code, message, nil)
}

func errorWithStructuredContent(code, message string, fields map[string]any) *mcp.CallToolResult {
	res := mcp.NewToolResultError(message)
	structured := map[string]any{"code": code}
	for key, value := range fields {
		if key == "code" || value == nil {
			continue
		}
		if text, ok := value.(string); ok && text == "" {
			continue
		}
		structured[key] = value
	}
	res.StructuredContent = structured
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

// upstreamError wraps a SigNoz backend client error with the uniform text prefix
// and the most specific structured code we can derive from the HTTP response.
func upstreamError(err error) *mcp.CallToolResult {
	var statusErr *signozclient.HTTPStatusError
	if !errors.As(err, &statusErr) {
		return errorWithCode(CodeUpstreamError, fmt.Sprintf("%s %s", upstreamErrorPrefix, err.Error()))
	}

	upstreamCode, upstreamMessage, upstreamType, parsedUpstreamBody := parseUpstreamErrorBody(statusErr.Body)
	message := upstreamHTTPErrorMessage(err, statusErr, upstreamMessage, parsedUpstreamBody)
	fields := map[string]any{
		"status": statusErr.StatusCode,
	}
	if upstreamCode != "" {
		fields["upstreamCode"] = upstreamCode
	}
	if upstreamMessage != "" {
		fields["upstreamMessage"] = boundedErrorDetail(upstreamMessage)
	}
	if upstreamType != "" {
		fields["upstreamType"] = upstreamType
	}
	if statusErr.StatusCode == http.StatusUnauthorized {
		if _, ok := assistantAuthEnvelopeCodes[upstreamCode]; ok {
			fields["upstreamAuth"] = map[string]string{"code": upstreamCode}
		}
	}

	return errorWithStructuredContent(upstreamCodeForStatus(statusErr.StatusCode, upstreamType), message, fields)
}

func upstreamCodeForStatus(status int, upstreamType string) string {
	switch status {
	case http.StatusBadRequest:
		return CodeValidationFailed
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodePermissionDenied
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusNotImplemented:
		return CodeUnsupported
	case http.StatusUnavailableForLegalReasons:
		return CodeLicenseUnavailable
	case statusClientClosedConnection:
		return CodeCanceled
	case http.StatusGatewayTimeout:
		return CodeTimeout
	default:
		if status == http.StatusServiceUnavailable {
			switch upstreamType {
			case "canceled":
				return CodeCanceled
			case "timeout":
				return CodeTimeout
			}
		}
		return CodeUpstreamError
	}
}

func upstreamHTTPErrorMessage(err error, statusErr *signozclient.HTTPStatusError, upstreamMessage string, parsedUpstreamBody bool) string {
	statusText := upstreamHTTPStatusText(statusErr, upstreamMessage, parsedUpstreamBody)
	rawMessage := err.Error()
	if rawStatusText := statusErr.Error(); rawStatusText != "" && strings.Contains(rawMessage, rawStatusText) {
		rawMessage = strings.Replace(rawMessage, rawStatusText, statusText, 1)
	} else {
		rawMessage = statusText
	}
	return fmt.Sprintf("%s %s", upstreamErrorPrefix, rawMessage)
}

func upstreamHTTPStatusText(statusErr *signozclient.HTTPStatusError, upstreamMessage string, parsedUpstreamBody bool) string {
	message := fmt.Sprintf("unexpected status %d", statusErr.StatusCode)
	detail := upstreamMessage
	if detail == "" && !parsedUpstreamBody {
		detail = statusErr.Body
	}
	if detail = boundedErrorDetail(detail); detail != "" {
		return fmt.Sprintf("%s: %s", message, detail)
	}
	return message
}

func boundedErrorDetail(detail string) string {
	return logpkg.TruncBody([]byte(strings.TrimSpace(detail)))
}

func parseUpstreamErrorBody(body string) (upstreamCode, upstreamMessage, upstreamType string, parsed bool) {
	var envelope struct {
		Error     json.RawMessage `json:"error"`
		ErrorType string          `json:"errorType"`
		Type      string          `json:"type"`
		Code      string          `json:"code"`
		Message   string          `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return "", "", "", false
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		var nested struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(envelope.Error, &nested); err == nil {
			upstreamType = nested.Type
			upstreamCode = nested.Code
			upstreamMessage = nested.Message
		} else {
			var message string
			if err := json.Unmarshal(envelope.Error, &message); err == nil {
				upstreamMessage = message
			}
		}
	}
	if upstreamType == "" {
		upstreamType = envelope.Type
	}
	if upstreamType == "" {
		upstreamType = envelope.ErrorType
	}
	if upstreamCode == "" {
		upstreamCode = envelope.Code
	}
	if upstreamMessage == "" {
		upstreamMessage = envelope.Message
	}
	return upstreamCode, upstreamMessage, upstreamType, true
}

// notFoundError marks a referenced resource that does not exist. The message is
// the human-readable explanation; the NOT_FOUND code lets clients avoid a blind
// retry and re-discover the id instead.
func notFoundError(message string) *mcp.CallToolResult {
	return errorWithCode(CodeNotFound, message)
}

// structuredResult is the success-path wrapper for tools whose output JSON is
// CODE-CONTROLLED — this server builds the envelope, so the same JSON is carried
// in BOTH the text block (block 0, for back-compat) and StructuredContent. Raw
// QB passthrough tools (search/aggregate/query_metrics) instead return the
// backend's variable, upstream-owned JSON verbatim and stay text-only.
//
// jsonPayload must be the exact bytes placed in the text block so the two
// representations never diverge. It is decoded with a json.Number-mode decoder
// so large SigNoz integers (> 2^53) are preserved exactly — a plain unmarshal
// into `any` routes numbers through float64 and would silently round them,
// making StructuredContent disagree with the byte-faithful text block.
func structuredResult(jsonPayload []byte) *mcp.CallToolResult {
	dec := json.NewDecoder(bytes.NewReader(jsonPayload))
	dec.UseNumber()
	var structured any
	if err := dec.Decode(&structured); err != nil {
		// Fail open: invalid JSON (shouldn't happen for a code-controlled tool)
		// falls back to a plain text result rather than erroring.
		return mcp.NewToolResultText(string(jsonPayload))
	}
	// Require exactly one JSON value: trailing junk or a second value would make
	// `structured` cover only part of block 0, so fail open to text-only.
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
