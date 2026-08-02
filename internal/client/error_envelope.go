package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const (
	maxErrorEnvelopeParseBytes = 1 << 20
	maxErrorDetailsBytes       = 16 << 10
	maxErrorDetails            = 5
	maxErrorSuggestions        = 5
	maxErrorTokenBytes         = 128
	maxErrorURLBytes           = 2 << 10
	redactedText               = "［REDACTED］"
	redactedURLText            = "［REDACTED URL］"
)

var (
	errorTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][-A-Za-z0-9._:]*$`)

	// Keep filtering deliberately high-confidence. Unrecognized bodies never
	// reach this path, so the remaining job is to remove obvious credentials
	// without rewriting ordinary renderer guidance.
	credentialNamePattern            = `(?:authorization|api[._-]?key|access[._-]?token|refresh[._-]?token|token|password|secret|cookie|session(?:[._-]?(?:id|token))?)`
	strongCredentialNamePattern      = `(?:api[._-]?key|access[._-]?token|refresh[._-]?token|password|secret|cookie|session(?:[._-]?(?:id|token))?)`
	secretTokenPattern               = regexp.MustCompile(`(?i)(^|[-._:])` + credentialNamePattern + `[:=]`)
	highConfidenceSecretTokenPattern = regexp.MustCompile(`(?i)^(?:sk_(?:live|test|prod)_[A-Za-z0-9_-]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|AKIA[A-Z0-9]{12,}|eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)$`)

	// Structured schemes can contain comma-separated credential components;
	// remove the complete line. Simpler schemes stop at prose delimiters.
	structuredAuthorizationSecretPattern = regexp.MustCompile(`(?i)(["']?\bauthorization\b["']?[ \t]*[:=][ \t]*["']?(?:digest|aws4-hmac-sha256)[ \t]+)[^\r\n]+`)
	authorizationSchemeSecretPattern     = regexp.MustCompile(`(?i)(["']?\bauthorization\b["']?[ \t]*[:=][ \t]*["']?(?:basic|bearer|token|apikey)[ \t]+)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^,;\r\n]+)`)
	authorizationQuotedSecretPattern     = regexp.MustCompile(`(?i)(["']?\bauthorization\b["']?[ \t]*[:=][ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|["'][^,;\r\n]*)`)
	authSchemeSecretPattern              = regexp.MustCompile(`(?i)\b(basic|bearer)([ \t]+)(?:[A-Za-z0-9._~+/=-]{16,}|[A-Za-z0-9]{0,64}[-._~+/=][A-Za-z0-9._~+/=-]{3,})`)
	namedEqualsSecretPattern             = regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])["']?` + credentialNamePattern + `["']?[ \t]*=[ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^,;\r\n]+)`)
	namedColonSecretPattern              = regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])["']?` + strongCredentialNamePattern + `["']?[ \t]*:[ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^,;\r\n]+)`)
	jwtSecretPattern                     = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	knownSecretPrefixPattern             = regexp.MustCompile(`(?i)(?:sk_(?:live|test|prod)_[A-Za-z0-9_-]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|AKIA[A-Z0-9]{12,})`)
	proseURLPattern                      = regexp.MustCompile(`(?i)https?://[^[:space:]<>"']+`)
	sensitiveURLTextPattern              = regexp.MustCompile(`(?i)` + credentialNamePattern + `[=:]`)
	safeURLFragmentPattern               = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)
)

// UpstreamErrorBody is the bounded, client-safe subset of a recognized SigNoz
// {"status":"error",...} response. Recognition is positive: a modern
// renderer object, legacy errorType+error pair, or complete top-level renderer
// tuple is required, so generic proxy JSON is not treated as backend guidance.
type UpstreamErrorBody struct {
	Code        string
	Message     string
	Type        string
	URL         string
	Suggestions []string
	Details     []UpstreamErrorDetail
	Retry       *UpstreamErrorRetry
	Recognized  bool
	// ShapeDrift contains only optional renderer field names whose present
	// values did not match the supported bounded shape. Values never enter
	// this diagnostic channel.
	ShapeDrift []string
	// StatusError is true when the body is well-formed JSON with
	// status:"error", even if its remaining shape is not recognized. The
	// transport uses this to emit a distinct upstream-contract drift warning.
	StatusError bool
}

type UpstreamErrorDetail struct {
	Message     string   `json:"message,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type UpstreamErrorRetry struct {
	// Delay is the renderer's time.Duration value in nanoseconds.
	Delay int64 `json:"delay"`
}

type rawUpstreamError struct {
	Type        string          `json:"type"`
	Code        string          `json:"code"`
	Message     string          `json:"message"`
	URL         json.RawMessage `json:"url"`
	Errors      json.RawMessage `json:"errors"`
	Retry       json.RawMessage `json:"retry"`
	Suggestions json.RawMessage `json:"suggestions"`
}

// ParseUpstreamErrorBody extracts the stable fields used by MCP error shaping.
// It intentionally does not expose raw JSON. Oversized or unrecognized bodies
// return Recognized=false so callers use their local status-derived fallback.
func ParseUpstreamErrorBody(body string) UpstreamErrorBody {
	if len(body) == 0 || len(body) > maxErrorEnvelopeParseBytes {
		return UpstreamErrorBody{}
	}

	var envelope map[string]json.RawMessage
	if err := decodeUpstreamJSON([]byte(body), &envelope); err != nil || upstreamJSONString(envelope["status"]) != "error" {
		return UpstreamErrorBody{}
	}
	unrecognized := UpstreamErrorBody{StatusError: true}

	var raw rawUpstreamError
	nestedError := bytes.TrimSpace(envelope["error"])
	var nested map[string]json.RawMessage
	switch {
	case len(nestedError) > 0 && nestedError[0] == '{' && decodeUpstreamJSON(nestedError, &nested) == nil && completeRendererTuple(nested):
		// Current SigNoz renderer shape: status:error plus a complete nested
		// type/code/message tuple. Optional guidance stays on the same object.
		raw = rawUpstreamErrorFrom(nested)
	case upstreamJSONString(envelope["errorType"]) != "":
		legacyMessage := upstreamJSONString(envelope["error"])
		if strings.TrimSpace(legacyMessage) == "" {
			return unrecognized
		}
		raw = rawUpstreamError{
			Type:        upstreamJSONString(envelope["errorType"]),
			Code:        upstreamJSONString(envelope["code"]),
			Message:     legacyMessage,
			URL:         envelope["url"],
			Errors:      envelope["errors"],
			Retry:       envelope["retry"],
			Suggestions: envelope["suggestions"],
		}
	case completeRendererTuple(envelope):
		// Some renderer integrations unwrap the error object but retain the
		// complete required tuple; accept that verified shape, not message-only
		// proxy JSON.
		raw = rawUpstreamErrorFrom(envelope)
	default:
		return unrecognized
	}

	message := safeUpstreamErrorMessage(raw.Message)
	details, detailsValid := safeUpstreamErrorDetails(raw.Errors, message)
	upstreamURL, urlValid := safeUpstreamErrorURL(raw.URL)
	suggestions, suggestionsValid := safeUpstreamErrorSuggestions(raw.Suggestions)
	retry, retryValid := safeUpstreamErrorRetry(raw.Retry)
	shapeDrift := make([]string, 0, 4)
	for _, field := range []struct {
		name  string
		valid bool
	}{
		{name: "url", valid: urlValid},
		{name: "suggestions", valid: suggestionsValid},
		{name: "errors", valid: detailsValid},
		{name: "retry", valid: retryValid},
	} {
		if !field.valid {
			shapeDrift = append(shapeDrift, field.name)
		}
	}
	parsed := UpstreamErrorBody{
		Code:        safeUpstreamErrorToken(raw.Code),
		Message:     message,
		Type:        safeUpstreamErrorToken(raw.Type),
		URL:         upstreamURL,
		Suggestions: suggestions,
		Details:     details,
		Retry:       retry,
		Recognized:  true,
		StatusError: true,
		ShapeDrift:  shapeDrift,
	}
	return parsed
}

func rawUpstreamErrorFrom(object map[string]json.RawMessage) rawUpstreamError {
	return rawUpstreamError{
		Type:        upstreamJSONString(object["type"]),
		Code:        upstreamJSONString(object["code"]),
		Message:     upstreamJSONString(object["message"]),
		URL:         object["url"],
		Errors:      object["errors"],
		Retry:       object["retry"],
		Suggestions: object["suggestions"],
	}
}

func completeRendererTuple(object map[string]json.RawMessage) bool {
	return strings.TrimSpace(upstreamJSONString(object["type"])) != "" &&
		strings.TrimSpace(upstreamJSONString(object["code"])) != "" &&
		strings.TrimSpace(upstreamJSONString(object["message"])) != ""
}

func upstreamJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := decodeUpstreamJSON(raw, &value); err != nil {
		return ""
	}
	return value
}

func decodeUpstreamJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func safeUpstreamErrorDetails(raw json.RawMessage, mainMessage string) ([]UpstreamErrorDetail, bool) {
	if optionalUpstreamFieldAbsent(raw) {
		return nil, true
	}
	if len(raw) > maxErrorDetailsBytes {
		return nil, false
	}
	var entries []json.RawMessage
	if err := decodeUpstreamJSON(raw, &entries); err != nil {
		return nil, false
	}
	details := make([]UpstreamErrorDetail, 0, min(len(entries), maxErrorDetails))
	seen := make(map[string]struct{}, min(len(entries), maxErrorDetails))
	valid := true
	for _, entry := range entries {
		// Legacy query-service errors expose errors[] as strings. Modern
		// renderer entries are objects; decode their optional members
		// independently so drift in one suggestion does not erase the rest.
		detail := UpstreamErrorDetail{}
		if message := upstreamJSONString(entry); message != "" {
			detail.Message = safeUpstreamErrorMessage(message)
		} else {
			var object map[string]json.RawMessage
			if err := decodeUpstreamJSON(entry, &object); err != nil {
				valid = false
				continue
			}
			rawMessage, messagePresent := object["message"]
			rawSuggestions, suggestionsPresent := object["suggestions"]
			if !messagePresent && !suggestionsPresent {
				valid = false
				continue
			}
			if messagePresent {
				message := upstreamJSONString(rawMessage)
				if optionalUpstreamFieldAbsent(rawMessage) || message == "" {
					valid = false
				} else {
					detail.Message = safeUpstreamErrorMessage(message)
				}
			}
			var suggestionsValid bool
			detail.Suggestions, suggestionsValid = safeUpstreamErrorSuggestions(rawSuggestions)
			if !suggestionsValid {
				valid = false
			}
		}
		if detail.Message == "" && len(detail.Suggestions) == 0 {
			continue
		}
		if detail.Message == mainMessage && len(detail.Suggestions) == 0 {
			continue
		}
		key := detail.Message + "\x00" + strings.Join(detail.Suggestions, "\x00")
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if len(details) < maxErrorDetails {
			details = append(details, detail)
		}
	}
	return details, valid
}

func foldUpstreamErrorDetails(mainMessage string, details []UpstreamErrorDetail) string {
	seen := map[string]struct{}{mainMessage: {}}
	additional := make([]string, 0, len(details))
	for _, detail := range details {
		if detail.Message == "" {
			continue
		}
		if _, duplicate := seen[detail.Message]; duplicate {
			continue
		}
		seen[detail.Message] = struct{}{}
		additional = append(additional, detail.Message)
	}
	if len(additional) == 0 {
		return mainMessage
	}
	if mainMessage == "" {
		return logpkg.TruncBody([]byte(strings.Join(additional, "; ")))
	}
	return logpkg.TruncBody([]byte(mainMessage + " (" + strings.Join(additional, "; ") + ")"))
}

func safeUpstreamErrorSuggestions(raw json.RawMessage) ([]string, bool) {
	if optionalUpstreamFieldAbsent(raw) {
		return nil, true
	}
	if len(raw) > maxErrorDetailsBytes {
		return nil, false
	}
	var entries []json.RawMessage
	if err := decodeUpstreamJSON(raw, &entries); err != nil {
		return nil, false
	}
	seen := make(map[string]struct{}, min(len(entries), maxErrorSuggestions))
	suggestions := make([]string, 0, min(len(entries), maxErrorSuggestions))
	valid := true
	for _, entry := range entries {
		rawSuggestion := upstreamJSONString(entry)
		if rawSuggestion == "" {
			valid = false
			continue
		}
		suggestion := safeUpstreamErrorMessage(rawSuggestion)
		if suggestion == "" {
			continue
		}
		if _, duplicate := seen[suggestion]; duplicate {
			continue
		}
		seen[suggestion] = struct{}{}
		if len(suggestions) < maxErrorSuggestions {
			suggestions = append(suggestions, suggestion)
		}
	}
	return suggestions, valid
}

func safeUpstreamErrorRetry(raw json.RawMessage) (*UpstreamErrorRetry, bool) {
	if optionalUpstreamFieldAbsent(raw) {
		return nil, true
	}
	if len(raw) > maxErrorDetailsBytes {
		return nil, false
	}
	var object map[string]json.RawMessage
	if err := decodeUpstreamJSON(raw, &object); err != nil {
		return nil, false
	}
	rawDelay, present := object["delay"]
	if !present || optionalUpstreamFieldAbsent(rawDelay) {
		return nil, false
	}
	var number json.Number
	if err := decodeUpstreamJSON(rawDelay, &number); err == nil {
		delay, err := number.Int64()
		if err != nil || delay < 0 {
			return nil, false
		}
		return &UpstreamErrorRetry{Delay: delay}, true
	}
	delay, err := time.ParseDuration(upstreamJSONString(rawDelay))
	if err != nil || delay < 0 {
		return nil, false
	}
	return &UpstreamErrorRetry{Delay: int64(delay)}, true
}

func optionalUpstreamFieldAbsent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func safeUpstreamErrorToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxErrorTokenBytes || !errorTokenPattern.MatchString(value) || secretTokenPattern.MatchString(value) || highConfidenceSecretTokenPattern.MatchString(value) || jwtSecretPattern.MatchString(value) || knownSecretPrefixPattern.MatchString(value) {
		return ""
	}
	return value
}

func safeUpstreamErrorURL(raw json.RawMessage) (string, bool) {
	if optionalUpstreamFieldAbsent(raw) {
		return "", true
	}
	value := upstreamJSONString(raw)
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxErrorURLBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return "", false
	}
	// Renderer URLs are documentation links, not signed requests. Query and
	// fragment data are unnecessary for recovery and can hide credentials in
	// aliases, nested redirects, signing fields, or future key spellings.
	if parsed.ForceQuery || parsed.RawQuery != "" || unsafeURLComponent(parsed.Hostname()) || unsafeURLComponent(parsed.Path) {
		return "", true
	}
	// SigNoz documentation links legitimately use simple section anchors.
	// Permit only decoded slug fragments; assignment syntax and encoded
	// fragments are capable of carrying access tokens or nested URLs.
	if parsed.Fragment != "" && (!safeURLFragmentPattern.MatchString(parsed.Fragment) || parsed.RawFragment != "" || unsafeURLComponent(parsed.Fragment)) {
		return "", true
	}
	parsed.RawPath = ""
	return parsed.String(), true
}

func unsafeURLComponent(value string) bool {
	if strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.ContainsAny(value, `<>"'`) || sensitiveURLTextPattern.MatchString(value) || jwtSecretPattern.MatchString(value) || knownSecretPrefixPattern.MatchString(value) {
		return true
	}
	decoded, err := url.PathUnescape(value)
	return err == nil && decoded != value && (strings.ContainsAny(decoded, `<>"'`) || sensitiveURLTextPattern.MatchString(decoded) || jwtSecretPattern.MatchString(decoded) || knownSecretPrefixPattern.MatchString(decoded))
}

func safeUpstreamErrorMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value)
	value = sanitizeProseURLs(value)
	value = structuredAuthorizationSecretPattern.ReplaceAllString(value, `${1}`+redactedText)
	value = authorizationSchemeSecretPattern.ReplaceAllString(value, `${1}`+redactedText)
	value = authorizationQuotedSecretPattern.ReplaceAllString(value, `${1}`+redactedText)
	value = authSchemeSecretPattern.ReplaceAllString(value, `$1$2`+redactedText)
	value = namedEqualsSecretPattern.ReplaceAllString(value, `${1}`+redactedText)
	value = namedColonSecretPattern.ReplaceAllString(value, `${1}`+redactedText)
	value = jwtSecretPattern.ReplaceAllString(value, redactedText)
	value = knownSecretPrefixPattern.ReplaceAllString(value, redactedText)
	// MCP text is Markdown. Neutralize active markup after credential parsing
	// so bracket-wrapped field names cannot hide from the filters. Fullwidth
	// source brackets and redaction markers stay inert even next to a URL.
	value = strings.NewReplacer("<", "&lt;", ">", "&gt;", "[", "［", "]", "］").Replace(value)
	return logpkg.TruncBody([]byte(value))
}

func sanitizeProseURLs(value string) string {
	return proseURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		core := raw
		suffix := ""
		for len(core) > 0 && strings.ContainsRune(".,;:)}", rune(core[len(core)-1])) {
			suffix = core[len(core)-1:] + suffix
			core = core[:len(core)-1]
		}
		parsed, err := url.Parse(core)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
			return redactedURLText + suffix
		}
		if unsafeURLComponent(parsed.Hostname()) || unsafeURLComponent(parsed.Path) {
			return redactedURLText + suffix
		}
		// Prose URLs are not the validated renderer documentation channel. Remove
		// query and fragment material, which can carry signed credentials.
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.RawFragment = ""
		return parsed.String() + suffix
	})
}

// ClientSafeText renders every recognized guidance field for MCP surfaces that
// only have a text error channel (notably resources). Structured tool results
// additionally expose these fields independently.
func (e UpstreamErrorBody) ClientSafeText() string {
	parts := make([]string, 0, 4+len(e.Details))
	if message := foldUpstreamErrorDetails(e.Message, e.Details); message != "" {
		parts = append(parts, message)
	}
	if e.URL != "" {
		parts = append(parts, "Documentation: "+e.URL)
	}
	if len(e.Suggestions) > 0 {
		parts = append(parts, "Suggestions: "+strings.Join(e.Suggestions, "; "))
	}
	for _, detail := range e.Details {
		if len(detail.Suggestions) == 0 {
			continue
		}
		context := "error detail"
		if detail.Message != "" {
			context = fmt.Sprintf("%q", detail.Message)
		}
		parts = append(parts, "Suggestions for "+context+": "+strings.Join(detail.Suggestions, "; "))
	}
	if e.Retry != nil {
		parts = append(parts, fmt.Sprintf("Retry delay: %s (%d ns)", time.Duration(e.Retry.Delay), e.Retry.Delay))
	}
	return strings.Join(parts, ". ")
}
