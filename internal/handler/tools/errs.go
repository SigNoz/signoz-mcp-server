package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
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

// maxUpstreamErrorDetails bounds how many error.errors[] detail messages are folded
// into the surfaced upstream message; the body is upstream-controlled input.
const maxUpstreamErrorDetails = 5

// maxUpstreamErrorDetailsBytes bounds how large an error object has its
// error.errors[] detail array extracted at all: json.RawMessage copies the
// field's bytes during Unmarshal and a non-2xx body can be up to 64 MiB, so
// parseUpstreamErrorBody only names errors[] in a decode target when the whole
// error object fits this bound (fail open, like a drifted shape — details
// dropped, main fields kept). 16 KiB is orders of magnitude more than the five
// surfaced details ever need.
const maxUpstreamErrorDetailsBytes = 16 << 10

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

// logUpstreamFailure logs a failed outbound call (SigNoz backend, template
// fetch) at a severity derived from its cause: context.Canceled — the MCP
// client disconnected or aborted the request mid-flight — logs at DEBUG with a
// cancellation note, while everything else, including context.DeadlineExceeded
// (a real operational signal), stays ERROR. The record is always emitted at
// the chosen level, never dropped (fail open, never fail silent).
func (h *Handler) logUpstreamFailure(ctx context.Context, msg string, err error, attrs ...slog.Attr) {
	level := logpkg.LevelForError(err)
	if level != slog.LevelError {
		msg += " (request cancelled by client)"
	}
	args := make([]any, 0, len(attrs)+1)
	for _, attr := range attrs {
		args = append(args, attr)
	}
	args = append(args, logpkg.ErrAttr(err))
	h.logger.Log(ctx, level, msg, args...)
}

// keyNotFoundPattern matches the QB v5 key-resolution failure ("key `service.name`
// not found") in a 400 body. The wording is stable across backend generations: older
// releases inline it in error.message, newer ones carry it in error.errors[].message
// — matching the raw body covers both. Contract-sensitive by design: if the backend
// rewords it, detection fails open (no enrichment) and logQueryFailure's ERROR-level
// fallback is the drift signal.
var keyNotFoundPattern = regexp.MustCompile("key `([^`]+)` not found")

// The 400 body is upstream-controlled input (buffered up to 64 MiB), so everything
// derived from it is bounded: only the first missingFilterKeyScanBytes are scanned
// (FindAllStringSubmatch's match cap would not stop it walking the whole body, and
// each failure is scanned by both logQueryFailure and upstreamQueryError), at most
// missingFilterKeyScanLimit matches are examined, a captured key longer than
// missingFilterKeyMaxLen is discarded as garbage, and at most
// missingFilterKeysLimit distinct keys are surfaced. Real key-not-found bodies are
// far smaller than the scan window; a phrase beyond it fails open (no enrichment).
const (
	missingFilterKeysLimit    = 10
	missingFilterKeyScanLimit = 64
	missingFilterKeyMaxLen    = 256
	missingFilterKeyScanBytes = 16 << 10
)

// missingFilterKeys extracts the filter keys a QB v5 400 reported as absent from
// the workspace's field metadata. Returns nil for anything other than an HTTP 400
// whose body carries the key-not-found wording.
func missingFilterKeys(err error) []string {
	var statusErr *signozclient.HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadRequest {
		return nil
	}
	body := statusErr.Body
	if len(body) > missingFilterKeyScanBytes {
		body = body[:missingFilterKeyScanBytes]
	}
	matches := keyNotFoundPattern.FindAllStringSubmatch(body, missingFilterKeyScanLimit)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, missingFilterKeysLimit)
	keys := make([]string, 0, missingFilterKeysLimit)
	for _, m := range matches {
		key := m[1]
		if len(key) > missingFilterKeyMaxLen {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
		if len(keys) == missingFilterKeysLimit {
			break
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

// missingKeyGuidance builds the recovery instructions appended to a key-not-found
// error. Field keys are workspace- and signal-specific: logs carry no spec-mandated
// resource attributes (even service.name is only present when the pipeline sets it),
// so the guidance steers the agent to discover real keys instead of retrying blind.
func missingKeyGuidance(keys []string, signal string) string {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = "`" + key + "`"
	}

	var b strings.Builder
	noun := "this workspace's data"
	if signal != "" {
		noun = "this workspace's " + signal + " data"
	}
	verb := "does"
	if len(keys) > 1 {
		verb = "do"
	}
	fmt.Fprintf(&b, "The filter references %s, which %s not exist in %s.", strings.Join(quoted, ", "), verb, noun)
	if signal == "logs" {
		b.WriteString(" Log attributes are workspace-specific: logs have no spec-mandated resource attributes, so even standard keys like service.name are only present when the log pipeline sets them.")
	}
	if signal != "" {
		fmt.Fprintf(&b, ` Discover valid keys with signoz_get_field_keys (signal=%q; fieldContext="resource" for resource attributes), then retry with an existing key`, signal)
	} else {
		b.WriteString(` Discover valid keys with signoz_get_field_keys for the queried signal (fieldContext="resource" for resource attributes), then retry with an existing key`)
	}
	if signal == "logs" {
		b.WriteString(" (for service scoping, workspaces without service.name often carry k8s.deployment.name or k8s.container.name)")
	}
	b.WriteString(", or remove the failing condition.")
	return b.String()
}

// upstreamQueryError is upstreamError for the QB v5 passthrough tools: when the 400
// reports filter keys missing from the workspace's field metadata, it appends
// signal-aware recovery guidance to the text block and surfaces the keys as
// `missingKeys` in StructuredContent so clients can branch without string-matching.
// signal is "logs"/"traces", or "" when the tool spans signals (execute_builder_query).
func upstreamQueryError(err error, signal string) *mcp.CallToolResult {
	res := upstreamError(err)
	keys := missingFilterKeys(err)
	if len(keys) == 0 {
		return res
	}
	if len(res.Content) == 1 {
		if tc, ok := res.Content[0].(mcp.TextContent); ok {
			tc.Text += "\n\n" + missingKeyGuidance(keys, signal)
			res.Content[0] = tc
		}
	}
	if structured, ok := res.StructuredContent.(map[string]any); ok {
		structured["missingKeys"] = keys
	}
	return res
}

// logQueryFailure is the QB v5 tools' variant of logUpstreamFailure: a 400 whose
// filter references keys absent from the workspace's metadata is an expected agent
// mistake (the tool result carries the recovery guidance), so it logs at WARN with
// the missing keys attached — still always emitted, never dropped. Everything else
// keeps logUpstreamFailure's severity contract.
func (h *Handler) logQueryFailure(ctx context.Context, msg string, err error, attrs ...slog.Attr) {
	keys := missingFilterKeys(err)
	if len(keys) == 0 {
		h.logUpstreamFailure(ctx, msg, err, attrs...)
		return
	}
	args := make([]any, 0, len(attrs)+2)
	for _, attr := range attrs {
		args = append(args, attr)
	}
	args = append(args, slog.Any("missingKeys", keys), logpkg.ErrAttr(err))
	h.logger.Log(ctx, slog.LevelWarn, msg+" (filter references keys missing from workspace field metadata)", args...)
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
			// Newer backends put the per-term detail in error.errors[] and keep
			// error.message as a bare summary ("Found N errors while parsing the
			// search expression."); fold the details in so they reach the caller.
			// Extracted in a separate size-gated pass: json.RawMessage copies the
			// field's bytes during Unmarshal, so an oversized error object must
			// never name errors[] in a decode target at all (fail open on the
			// details; the main fields above are already decoded).
			if len(envelope.Error) <= maxUpstreamErrorDetailsBytes {
				var withDetails struct {
					Errors json.RawMessage `json:"errors"`
				}
				if err := json.Unmarshal(envelope.Error, &withDetails); err == nil {
					switch details := upstreamErrorDetails(withDetails.Errors, upstreamMessage); {
					case len(details) == 0:
					case upstreamMessage == "":
						upstreamMessage = strings.Join(details, "; ")
					default:
						upstreamMessage = upstreamMessage + " (" + strings.Join(details, "; ") + ")"
					}
				}
			}
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

// upstreamErrorDetails decodes error.errors[] best-effort: the documented shape is
// [{"message": "..."}], with a plain []string fallback; any other shape — or a
// payload over maxUpstreamErrorDetailsBytes — yields nil rather than an error, so
// a drifted or oversized detail array can never discard the main error fields
// (they are decoded independently). The caller size-gates the whole error object
// before raw is ever copied out of it, so the size check here is defense in
// depth. Details are trimmed, deduplicated (including against the main message),
// and capped.
func upstreamErrorDetails(raw json.RawMessage, mainMessage string) []string {
	if len(raw) == 0 || len(raw) > maxUpstreamErrorDetailsBytes {
		return nil
	}
	var messages []string
	var structured []struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &structured); err == nil {
		messages = make([]string, 0, len(structured))
		for _, additional := range structured {
			messages = append(messages, additional.Message)
		}
	} else if err := json.Unmarshal(raw, &messages); err != nil {
		return nil
	}

	seen := map[string]struct{}{mainMessage: {}}
	details := make([]string, 0, maxUpstreamErrorDetails)
	for _, message := range messages {
		detail := strings.TrimSpace(message)
		if detail == "" {
			continue
		}
		if _, dup := seen[detail]; dup {
			continue
		}
		seen[detail] = struct{}{}
		details = append(details, detail)
		if len(details) == maxUpstreamErrorDetails {
			break
		}
	}
	return details
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
