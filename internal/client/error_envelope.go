package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

const (
	maxErrorEnvelopeBytes = 64 << 10
	maxErrorArrayBytes    = 16 << 10
	maxErrorArrayItems    = 5
	maxErrorTokenBytes    = 128
	maxErrorURLBytes      = 2 << 10
	maxEnvelopeProbeBytes = 256
)

var (
	errorTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][-A-Za-z0-9._:]*$`)

	credentialAssignmentPattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:signoz[._-]?api[._-]?key|api[._-]?key|client[._-]?secret|access[._-]?token|refresh[._-]?token|authorization|password|passwd)\s*=\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	quotedCredentialPattern     = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:signoz[._-]?api[._-]?key|api[._-]?key|client[._-]?secret|access[._-]?token|refresh[._-]?token|authorization|password|passwd)\s*:\s*(?:"[^"]+"|'[^']+')`)
	credentialTokenPattern      = regexp.MustCompile(`(?i)^(?:signoz[._-]?api[._-]?key|api[._-]?key|client[._-]?secret|access[._-]?token|refresh[._-]?token|authorization|password|passwd|secret|cookie|session(?:[._-]?(?:id|token))?):[-A-Za-z0-9._~+/]{4,}$`)
	authorizationHeaderPattern  = regexp.MustCompile(`(?i)\bauthorization\s*:\s*(?:basic|bearer|digest|token|apikey|aws4-hmac-sha256)\s+[A-Za-z0-9._~+/=-]{8,}`)
	authorizationTokenPattern   = regexp.MustCompile(`(?i)\b(?:basic|bearer|digest|token|apikey|aws4-hmac-sha256)\s+([A-Za-z0-9._~+/=-]{8,})`)
	cookieHeaderPattern         = regexp.MustCompile(`(?i)\bcookie\s*:\s*[^\r\n]*=`)
	jwtPattern                  = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	knownSecretPattern          = regexp.MustCompile(`(?i)\b(?:sk_(?:live|test|prod)_[A-Za-z0-9_-]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|gh[pousr]_[A-Za-z0-9]{8,}|AKIA[A-Z0-9]{12,})\b`)
	activeMarkupPattern         = regexp.MustCompile(`(?is)<\s*/?\s*(?:a|audio|base|body|button|embed|form|frame|frameset|head|html|iframe|img|input|link|math|meta|object|script|source|style|svg|template|video)\b[^>]*>|<\s*/?\s*[a-z][a-z0-9-]*\s+[^>]+>|!?\[[^\]\r\n]*\]\([^\)\r\n]*\)`)
	oversizedRendererPattern    = regexp.MustCompile(`^\s*\{\s*"status"\s*:\s*"error"\s*,\s*"error"\s*:\s*\{`)
)

// UpstreamErrorBody is the bounded, client-safe subset of a positively
// recognized SigNoz error response.
type UpstreamErrorBody struct {
	Code        string
	Type        string
	Message     string
	URL         string
	Suggestions []string
	Details     []UpstreamErrorDetail
	Retry       *UpstreamErrorRetry
	Recognized  bool
	StatusError bool
	DriftFields []string
}

type UpstreamErrorDetail struct {
	Message     string   `json:"message,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type UpstreamErrorRetry struct {
	// Delay is the renderer's time.Duration value in nanoseconds.
	Delay int64 `json:"delay"`
}

// ParseUpstreamErrorBody recognizes the verified SigNoz renderer envelope and
// the legacy query-service error envelope. It never exposes raw response JSON.
func ParseUpstreamErrorBody(body string) UpstreamErrorBody {
	if len(body) == 0 {
		return UpstreamErrorBody{}
	}
	if len(body) > maxErrorEnvelopeBytes {
		probe := body[:min(len(body), maxEnvelopeProbeBytes)]
		if oversizedRendererPattern.MatchString(probe) {
			return UpstreamErrorBody{StatusError: true, DriftFields: []string{"envelope"}}
		}
		return UpstreamErrorBody{}
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return UpstreamErrorBody{}
	}
	status, ok := rawJSONString(envelope["status"])
	if !ok || status != "error" {
		return UpstreamErrorBody{}
	}

	parsed := UpstreamErrorBody{StatusError: true}
	rawError, hasError := envelope["error"]
	trimmedError := bytes.TrimSpace(rawError)
	if hasError && len(trimmedError) > 0 && trimmedError[0] == '{' {
		var renderer map[string]json.RawMessage
		if err := json.Unmarshal(trimmedError, &renderer); err != nil {
			parsed.DriftFields = []string{"error"}
			return parsed
		}

		code, codeOK := requiredJSONString(renderer["code"])
		message, messageOK := requiredJSONString(renderer["message"])
		if !codeOK {
			parsed.DriftFields = append(parsed.DriftFields, "code")
		}
		if !messageOK {
			parsed.DriftFields = append(parsed.DriftFields, "message")
		}
		if !codeOK || !messageOK {
			return parsed
		}

		parsed.Recognized = true
		parsed.Code = safeErrorToken(code)
		parsed.Message = safeGuidanceText(message)
		if parsed.Code == "" {
			parsed.addDrift("code")
		}
		if parsed.Message == "" {
			parsed.addDrift("message")
		}
		parseRendererOptionals(renderer, &parsed)
		return parsed
	}

	errorType, typeOK := requiredJSONString(envelope["errorType"])
	legacyMessage, messageOK := requiredJSONString(rawError)
	if typeOK && messageOK {
		parsed.Recognized = true
		parsed.Type = safeErrorToken(errorType)
		parsed.Message = safeGuidanceText(legacyMessage)
		if parsed.Type == "" {
			parsed.addDrift("errorType")
		}
		if parsed.Message == "" {
			parsed.addDrift("error")
		}
		return parsed
	}

	parsed.DriftFields = []string{"envelope"}
	return parsed
}

func parseRendererOptionals(renderer map[string]json.RawMessage, parsed *UpstreamErrorBody) {
	if raw, present := renderer["type"]; present && !rawJSONNull(raw) {
		value, ok := rawJSONString(raw)
		if !ok {
			parsed.addDrift("type")
		} else {
			parsed.Type = safeErrorToken(value)
			if strings.TrimSpace(value) != "" && parsed.Type == "" {
				parsed.addDrift("type")
			}
		}
	}

	if raw, present := renderer["url"]; present && !rawJSONNull(raw) {
		value, ok := rawJSONString(raw)
		if !ok {
			parsed.addDrift("url")
		} else {
			parsed.URL = safeErrorURL(value)
			if strings.TrimSpace(value) != "" && parsed.URL == "" {
				parsed.addDrift("url")
			}
		}
	}

	suggestions, suggestionsValid := parseErrorSuggestions(renderer["suggestions"])
	parsed.Suggestions = suggestions
	if !suggestionsValid {
		parsed.addDrift("suggestions")
	}

	details, detailsValid := parseErrorDetails(renderer["errors"], parsed.Message)
	parsed.Details = details
	if !detailsValid {
		parsed.addDrift("errors")
	}

	if retry, valid := parseErrorRetry(renderer["retry"]); !valid {
		parsed.addDrift("retry")
	} else {
		parsed.Retry = retry
	}
}

func parseErrorSuggestions(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 || rawJSONNull(raw) {
		return nil, true
	}
	if len(raw) > maxErrorArrayBytes {
		return nil, false
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}

	suggestions := make([]string, 0, min(len(entries), maxErrorArrayItems))
	seen := make(map[string]struct{}, min(len(entries), maxErrorArrayItems))
	valid := true
	for _, entry := range entries {
		value, ok := rawJSONString(entry)
		if !ok {
			valid = false
			continue
		}
		safeValue := safeGuidanceText(value)
		if safeValue == "" {
			if strings.TrimSpace(value) != "" {
				valid = false
			}
			continue
		}
		if _, duplicate := seen[safeValue]; duplicate {
			continue
		}
		seen[safeValue] = struct{}{}
		if len(suggestions) < maxErrorArrayItems {
			suggestions = append(suggestions, safeValue)
		}
	}
	return suggestions, valid
}

func parseErrorDetails(raw json.RawMessage, mainMessage string) ([]UpstreamErrorDetail, bool) {
	if len(raw) == 0 || rawJSONNull(raw) {
		return nil, true
	}
	if len(raw) > maxErrorArrayBytes {
		return nil, false
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}

	details := make([]UpstreamErrorDetail, 0, min(len(entries), maxErrorArrayItems))
	seen := make(map[string]struct{}, min(len(entries), maxErrorArrayItems))
	valid := true
	for _, entry := range entries {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry, &object); err != nil {
			valid = false
			continue
		}

		detail := UpstreamErrorDetail{}
		rawMessage, present := object["message"]
		message, messageOK := rawJSONString(rawMessage)
		if !present || !messageOK {
			valid = false
		} else {
			detail.Message = safeGuidanceText(message)
			if strings.TrimSpace(message) != "" && detail.Message == "" {
				valid = false
			}
		}

		suggestions, suggestionsOK := parseErrorSuggestions(object["suggestions"])
		detail.Suggestions = suggestions
		if !suggestionsOK {
			valid = false
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
		if len(details) < maxErrorArrayItems {
			details = append(details, detail)
		}
	}
	return details, valid
}

func parseErrorRetry(raw json.RawMessage) (*UpstreamErrorRetry, bool) {
	if len(raw) == 0 || rawJSONNull(raw) {
		return nil, true
	}
	if len(raw) > maxErrorArrayBytes {
		return nil, false
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false
	}
	rawDelay, present := object["delay"]
	if !present || rawJSONNull(rawDelay) {
		return nil, false
	}
	delay, err := strconv.ParseInt(string(bytes.TrimSpace(rawDelay)), 10, 64)
	if err != nil || delay < 0 {
		return nil, false
	}
	return &UpstreamErrorRetry{Delay: delay}, true
}

func requiredJSONString(raw json.RawMessage) (string, bool) {
	value, ok := rawJSONString(raw)
	return value, ok && strings.TrimSpace(value) != ""
}

func rawJSONString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || rawJSONNull(raw) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func rawJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func safeErrorToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxErrorTokenBytes || !errorTokenPattern.MatchString(value) || credentialTokenPattern.MatchString(value) || unsafeGuidance(value) {
		return ""
	}
	return value
}

func safeGuidanceText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || unsafeGuidance(value) {
		return ""
	}
	return logpkg.TruncBody([]byte(value))
}

func unsafeGuidance(value string) bool {
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0 {
		return true
	}
	return activeMarkupPattern.MatchString(value) ||
		credentialAssignmentPattern.MatchString(value) ||
		quotedCredentialPattern.MatchString(value) ||
		authorizationHeaderPattern.MatchString(value) ||
		containsLikelyAuthorizationToken(value) ||
		cookieHeaderPattern.MatchString(value) ||
		jwtPattern.MatchString(value) ||
		knownSecretPattern.MatchString(value)
}

func containsLikelyAuthorizationToken(value string) bool {
	for _, match := range authorizationTokenPattern.FindAllStringSubmatch(value, -1) {
		candidate := match[1]
		if strings.IndexFunc(candidate, func(r rune) bool {
			return !unicode.IsLetter(r)
		}) >= 0 {
			return true
		}
	}
	return false
}

func safeErrorURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxErrorURLBytes || unsafeGuidance(value) {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return ""
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ""
	}
	for key, values := range query {
		if sensitiveURLKey(key) {
			return ""
		}
		for _, queryValue := range values {
			if unsafeGuidance(queryValue) {
				return ""
			}
		}
	}
	if unsafeGuidance(parsed.Fragment) {
		return ""
	}
	return value
}

func sensitiveURLKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	switch normalized {
	case "authorization", "signozapikey", "apikey", "clientsecret", "accesstoken", "refreshtoken", "token", "password", "passwd", "secret", "cookie", "session", "sessionid", "sessiontoken", "signature", "sig", "xamzcredential", "xamzsignature":
		return true
	default:
		return false
	}
}

func (e *UpstreamErrorBody) addDrift(field string) {
	for _, existing := range e.DriftFields {
		if existing == field {
			return
		}
	}
	e.DriftFields = append(e.DriftFields, field)
}

// FoldedMessage combines the summary and distinct detail messages for clients
// that only have a single text field. Structured details remain unchanged.
func (e UpstreamErrorBody) FoldedMessage() string {
	seen := map[string]struct{}{}
	if e.Message != "" {
		seen[e.Message] = struct{}{}
	}
	details := make([]string, 0, len(e.Details))
	for _, detail := range e.Details {
		if detail.Message == "" {
			continue
		}
		if _, duplicate := seen[detail.Message]; duplicate {
			continue
		}
		seen[detail.Message] = struct{}{}
		details = append(details, detail.Message)
	}
	if len(details) == 0 {
		return e.Message
	}
	if e.Message == "" {
		return logpkg.TruncBody([]byte(strings.Join(details, "; ")))
	}
	return logpkg.TruncBody([]byte(e.Message + " (" + strings.Join(details, "; ") + ")"))
}

// ClientSafeText renders recognized guidance without serializing the raw
// upstream envelope.
func (e UpstreamErrorBody) ClientSafeText() string {
	parts := make([]string, 0, 4+len(e.Details))
	if message := e.FoldedMessage(); message != "" {
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
	return logpkg.TruncBody([]byte(strings.Join(parts, ". ")))
}
