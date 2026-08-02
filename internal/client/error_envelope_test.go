package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseUpstreamErrorBody_RecognizesSigNozEnvelopes(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
		wantType    string
	}{
		{
			name:        "nested renderer envelope with details",
			body:        `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"summary","url":"https://signoz.io/docs/query-errors","suggestions":["narrow the query"],"errors":[{"message":"summary","suggestions":[]},{"message":"detail","suggestions":["use an existing key"]}],"retry":{"delay":5000000000}}}`,
			wantCode:    "invalid_input",
			wantMessage: "summary",
			wantType:    "invalid-input",
		},
		{
			name:        "legacy query envelope",
			body:        `{"status":"error","errorType":"exec","error":"query failed"}`,
			wantMessage: "query failed",
			wantType:    "exec",
		},
		{
			name:        "top-level renderer message",
			body:        `{"status":"error","code":"unauthenticated","type":"unauthorized","message":"invalid token"}`,
			wantCode:    "unauthenticated",
			wantMessage: "invalid token",
			wantType:    "unauthorized",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseUpstreamErrorBody(tc.body)
			if !got.Recognized {
				t.Fatalf("body was not recognized: %s", tc.body)
			}
			if got.Code != tc.wantCode || got.Message != tc.wantMessage || got.Type != tc.wantType {
				t.Fatalf("parsed = %#v, want code=%q message=%q type=%q", got, tc.wantCode, tc.wantMessage, tc.wantType)
			}
			if tc.name == "nested renderer envelope with details" {
				if got.URL != "https://signoz.io/docs/query-errors" || len(got.Suggestions) != 1 || got.Suggestions[0] != "narrow the query" {
					t.Fatalf("renderer-wide guidance was not preserved: %#v", got)
				}
				if len(got.Details) != 1 || len(got.Details[0].Suggestions) != 1 || got.Details[0].Suggestions[0] != "use an existing key" {
					t.Fatalf("detail guidance was not preserved: %#v", got.Details)
				}
				if got.Retry == nil || got.Retry.Delay != 5_000_000_000 {
					t.Fatalf("retry guidance was not preserved: %#v", got.Retry)
				}
				text := got.ClientSafeText()
				for _, want := range []string{"Documentation: https://signoz.io/docs/query-errors", "Suggestions: narrow the query", `Suggestions for "detail": use an existing key`, "Retry delay: 5s (5000000000 ns)"} {
					if !strings.Contains(text, want) {
						t.Fatalf("client-safe text missing %q: %s", want, text)
					}
				}
			}
		})
	}
}

func TestParseUpstreamErrorBody_RejectsUnrecognizedOrOversizedJSON(t *testing.T) {
	for _, body := range []string{
		`{"message":"proxy-body-canary"}`,
		`{"status":"error","message":"proxy-body-canary"}`,
		`{"status":"fail","message":"proxy-body-canary"}`,
		`["proxy-body-canary"]`,
		`null`,
		`{"status":"error"`,
		`{"status":"error","type":"exec","code":"failed","message":"first"} {"message":"trailing"}`,
		strings.Repeat("x", maxErrorEnvelopeParseBytes+1),
	} {
		if got := ParseUpstreamErrorBody(body); got.Recognized || got.Code != "" || got.Message != "" || got.Type != "" {
			t.Fatalf("unrecognized body produced client fields: %#v", got)
		}
	}
	if got := ParseUpstreamErrorBody(`{"status":"error","message":"future-shape"}`); !got.StatusError || got.Recognized {
		t.Fatalf("status:error drift marker = %#v, want StatusError only", got)
	}
}

func TestParseUpstreamErrorBody_OptionalFieldDriftFailsOpen(t *testing.T) {
	body := `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"summary","url":42,"suggestions":[{"future":"shape"},"kept suggestion"],"errors":[{"message":"kept detail","suggestions":{"future":"shape"}},42,{"message":"second detail","suggestions":[false,"kept detail suggestion"]}],"retry":{"delay":"2.5s","future":true}}}`
	got := ParseUpstreamErrorBody(body)
	if !got.Recognized || got.Code != "invalid_input" || got.Type != "invalid-input" || got.Message != "summary" {
		t.Fatalf("required renderer tuple was lost to optional drift: %#v", got)
	}
	if got.URL != "" {
		t.Fatalf("wrong-type URL survived: %q", got.URL)
	}
	if len(got.Suggestions) != 1 || got.Suggestions[0] != "kept suggestion" {
		t.Fatalf("valid suggestion was lost: %#v", got.Suggestions)
	}
	if len(got.Details) != 2 || got.Details[0].Message != "kept detail" || len(got.Details[0].Suggestions) != 0 || len(got.Details[1].Suggestions) != 1 || got.Details[1].Suggestions[0] != "kept detail suggestion" {
		t.Fatalf("valid detail fields were lost: %#v", got.Details)
	}
	if got.Retry == nil || got.Retry.Delay != 2_500_000_000 {
		t.Fatalf("duration-string retry guidance was lost: %#v", got.Retry)
	}
	if strings.Join(got.ShapeDrift, ",") != "url,suggestions,errors" {
		t.Fatalf("shape drift fields = %#v, want names only", got.ShapeDrift)
	}
}

func TestParseUpstreamErrorBody_DetectsDriftAfterOutputCaps(t *testing.T) {
	body := `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"summary","suggestions":["s1","s2","s3","s4","s5",false],"errors":[{"message":"d1"},{"message":"d2"},{"message":"d3"},{"message":"d4"},{"message":"d5"},{"future":"shape"}]}}`
	got := ParseUpstreamErrorBody(body)
	if !got.Recognized || len(got.Suggestions) != maxErrorSuggestions || len(got.Details) != maxErrorDetails {
		t.Fatalf("bounded valid guidance was not preserved: %#v", got)
	}
	if strings.Join(got.ShapeDrift, ",") != "suggestions,errors" {
		t.Fatalf("late shape drift was silent: %#v", got.ShapeDrift)
	}
}

func TestParseUpstreamErrorBody_BoundsAndFiltersClientFields(t *testing.T) {
	longCode := strings.Repeat("c", maxErrorTokenBytes+1)
	longType := strings.Repeat("t", maxErrorTokenBytes+1)
	longMessage := strings.Repeat("m", 5000) + "message-tail-canary"
	body := `{"status":"error","error":{"code":"` + longCode + `","type":"` + longType + `","message":"` + longMessage + `"}}`
	got := ParseUpstreamErrorBody(body)
	if !got.Recognized {
		t.Fatal("SigNoz envelope was not recognized")
	}
	if got.Code != "" || got.Type != "" {
		t.Fatalf("oversized code/type escaped: %#v", got)
	}
	if !strings.Contains(got.Message, "...(truncated)") || strings.Contains(got.Message, "message-tail-canary") {
		t.Fatalf("message was not bounded: %q", got.Message)
	}

	unsafe := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"authz_forbidden","type":"forbidden","message":"<script>Authorization: Basic basic-prefix-canary basic-suffix-canary\ntoken=token-canary password='quoted password canary'\nBearer bearer-canary</script>"}}`)
	if !unsafe.Recognized {
		t.Fatal("filtered SigNoz envelope was not recognized")
	}
	for _, leaked := range []string{"<script>", "basic-prefix-canary", "basic-suffix-canary", "bearer-canary", "token-canary", "quoted password canary"} {
		if strings.Contains(unsafe.Message, leaked) {
			t.Fatalf("filtered message leaked %q: %q", leaked, unsafe.Message)
		}
	}
	if !strings.Contains(unsafe.Message, redactedText) || !strings.Contains(unsafe.Message, "&lt;script&gt;") {
		t.Fatalf("message lacks visible filtering markers: %q", unsafe.Message)
	}

	credentialFields := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"api_key:sk_live_code_canary","type":"session:session-type-canary","message":"Cookie: session=session-cookie-canary; other=value\nsession_id=session-id-canary"}}`)
	if !credentialFields.Recognized {
		t.Fatal("renderer envelope with unsafe tokens was not recognized")
	}
	if credentialFields.Code != "" || credentialFields.Type != "" {
		t.Fatalf("credential-looking code/type escaped: %#v", credentialFields)
	}
	encoded := credentialFields.ClientSafeText()
	for _, leaked := range []string{"session-cookie-canary", "session-id-canary"} {
		if strings.Contains(encoded, leaked) {
			t.Fatalf("credential field leaked %q: %s", leaked, encoded)
		}
	}

	quotedAndDotted := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"api.key:code-secret-canary","type":"access.token:type-secret-canary","message":"headers={\"Authorization\":\"Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==\"}; api.key=message-secret-canary; contact your administrator","suggestions":["'password': 'suggestion-secret-canary'; retry after access is fixed"],"errors":[{"message":"session.id=detail-secret-canary; keep this detail","suggestions":["Cookie: sid=detail-cookie-canary; contact support"]}]}}`)
	if !quotedAndDotted.Recognized || quotedAndDotted.Code != "" || quotedAndDotted.Type != "" {
		t.Fatalf("quoted/dotted credential fields were not filtered: %#v", quotedAndDotted)
	}
	encodedQuoted, err := json.Marshal(quotedAndDotted)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"QWxhZGRpbjpvcGVuIHNlc2FtZQ==", "message-secret-canary", "suggestion-secret-canary", "detail-secret-canary", "detail-cookie-canary", "code-secret-canary", "type-secret-canary"} {
		if strings.Contains(string(encodedQuoted), leaked) {
			t.Fatalf("quoted/dotted credential leaked %q: %s", leaked, encodedQuoted)
		}
	}
	for _, preserved := range []string{"contact your administrator", "retry after access is fixed", "keep this detail", "contact support"} {
		if !strings.Contains(string(encodedQuoted), preserved) {
			t.Fatalf("post-secret guidance %q was lost: %s", preserved, encodedQuoted)
		}
	}

	natural := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"authz_forbidden","type":"forbidden","message":"authorization: user lacks role editor; invalid token: signature mismatch"}}`)
	if natural.Message != "authorization: user lacks role editor; invalid token: signature mismatch" {
		t.Fatalf("legitimate authorization guidance was over-redacted: %q", natural.Message)
	}
	diagnosticCodes := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"authz_forbidden","type":"forbidden","message":"authorization: insufficient_permissions; token: signature_mismatch; token: actualsecretmismatch"}}`)
	if !strings.Contains(diagnosticCodes.Message, "authorization: insufficient_permissions") || !strings.Contains(diagnosticCodes.Message, "token: signature_mismatch") || strings.Contains(diagnosticCodes.Message, "actualsecretmismatch") {
		t.Fatalf("diagnostic-code exception was too broad or too narrow: %q", diagnosticCodes.Message)
	}

	guidance := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"invalid_input","type":"invalid-input","message":"safe summary","url":"https://signoz.io/docs/query?token=url-secret-canary","suggestions":["<b>Bearer suggestion-secret-canary</b>","s2","s3","s4","s5","suggestion-over-cap-canary"],"errors":[{"message":"detail","suggestions":["password=detail-secret-canary"]}],"retry":{"delay":-1}}}`)
	if guidance.URL != "" || guidance.Retry != nil {
		t.Fatalf("unsafe URL or invalid retry escaped: %#v", guidance)
	}
	if len(guidance.Suggestions) != maxErrorSuggestions || strings.Contains(strings.Join(guidance.Suggestions, " "), "suggestion-over-cap-canary") {
		t.Fatalf("suggestion budget not enforced: %#v", guidance.Suggestions)
	}
	encodedGuidance, err := json.Marshal(guidance)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"url-secret-canary", "suggestion-secret-canary", "detail-secret-canary", "<b>"} {
		if strings.Contains(string(encodedGuidance), leaked) {
			t.Fatalf("renderer guidance leaked %q: %s", leaked, encodedGuidance)
		}
	}

	for _, unsafeURL := range []string{
		`https://signoz.io/docs?X-Amz-Signature=url-secret-canary`,
		`https://signoz.io/docs?sig=url-secret-canary`,
		`https://signoz.io/docs?redirect=https%3A%2F%2Fexample.test%2F%3Ftoken%3Durl-secret-canary`,
		`https://signoz.io/docs#access_token=url-secret-canary`,
		`https://sk_live_hostsecret123456.example.test/docs`,
		`https://example.test/docs/sk_live_pathsecret123456`,
		`https://example.test/docs/%3Cscript%3Ealert%281%29%3C%2Fscript%3E`,
	} {
		body, err := json.Marshal(map[string]any{"status": "error", "error": map[string]any{
			"type": "invalid-input", "code": "invalid_input", "message": "safe", "url": unsafeURL,
		}})
		if err != nil {
			t.Fatal(err)
		}
		parsed := ParseUpstreamErrorBody(string(body))
		if parsed.URL != "" || strings.Contains(parsed.ClientSafeText(), "url-secret-canary") {
			t.Fatalf("credential-bearing documentation URL survived: %#v", parsed)
		}
	}
}

func TestParseUpstreamErrorBody_FiltersCredentialVariantsAndActiveMarkup(t *testing.T) {
	message := strings.Join([]string{
		"SIGNOZ_API_KEY=env-secret-canary; keep env guidance",
		"HTTP_AUTHORIZATION: opaque-authorization-secret-canary; keep auth guidance",
		"MY_SESSION_ID=session-env-secret-canary; keep session guidance",
		`headers["Authorization"]="bracket-authorization-secret-canary"; keep bracket auth guidance`,
		`headers["api_key"]="bracket-api-secret-canary"; keep bracket API guidance`,
		"password: hunter2; keep short password guidance",
		"api.key: abc123; keep short API guidance",
		"session.id: s123; keep short session guidance",
		`password: "unterminated-short-secret-canary; keep unterminated password guidance`,
		`Authorization: Bearer "unterminated-bearer-secret-canary; keep unterminated bearer guidance`,
		"password=correct horse battery staple; keep multiword guidance",
		"invalid API key abc123short; keep contextual short guidance",
		"password supersecret; keep alphabetic password guidance",
		"API key secretvalue; keep alphabetic key guidance",
		"Authorization: abc123short; keep short authorization guidance",
		"invalid token: signature mismatch",
		`payload={"password":"prefix\\\"escaped-suffix-secret-canary"}; keep escaped guidance`,
		"Cookie: first=first-cookie-secret-canary; Secure; later=later-cookie-secret-canary; contact support",
		`Authorization: Digest username="Mufasa", realm="x", nonce="digest-nonce-secret-canary", response="digest-response-secret-canary"`,
		"keep digest guidance",
		`Digest algorithm=MD5, username="u", nonce="standalone-digest-secret-canary", response="standalone-response-secret-canary"`,
		"keep standalone digest guidance",
		`Authorization: AWS4-HMAC-SHA256 Credential=aws-credential-secret-canary, Signature=aws-signature-secret-canary, Security-Token=aws-token-secret-canary`,
		"keep AWS guidance",
		`Authorization: Bearer "opaque-bearer-secret-canary"; keep bearer guidance`,
		"invalid API key sk_live_contextualsecret123456; rotate it",
		"JWT eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJqd3Qtc2VjcmV0LWNhbmFyeSJ9.signaturesecret",
		"unsafe ![image](https://attacker.test/canary) and [link](javascript:alert(1))",
		"reference [click][ref] and ![pixel][ref]",
		"[ref]: javascript:alert(1)",
		`preescaped \[click\](javascript:alert(1)) and \![pixel\][ref]`,
	}, "\n")
	body, err := json.Marshal(map[string]any{
		"status": "error",
		"error": map[string]any{
			"type":    "wrapped_eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0eXBlIn0.typesecret",
			"code":    "wrapped_sk_live_codesecret123456",
			"message": message,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := ParseUpstreamErrorBody(string(body))
	if !got.Recognized || got.Code != "" || got.Type != "" {
		t.Fatalf("credential-shaped code/type survived: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	for _, leaked := range []string{
		"env-secret-canary", "opaque-authorization-secret-canary", "session-env-secret-canary",
		"bracket-authorization-secret-canary", "bracket-api-secret-canary",
		"hunter2", "abc123", "s123", "abc123short", "supersecret", "secretvalue", "unterminated-short-secret-canary", "unterminated-bearer-secret-canary", "correct horse battery staple",
		"escaped-suffix-secret-canary", "first-cookie-secret-canary", "later-cookie-secret-canary",
		"digest-nonce-secret-canary", "digest-response-secret-canary", "aws-credential-secret-canary",
		"standalone-digest-secret-canary", "standalone-response-secret-canary", "aws-signature-secret-canary", "aws-token-secret-canary", "sk_live_contextualsecret123456",
		"opaque-bearer-secret-canary", "qd3Qtc2VjcmV0LWNhbmFye", "sk_live_codesecret123456", "typesecret",
	} {
		if strings.Contains(wire, leaked) {
			t.Fatalf("credential variant leaked %q: %s", leaked, wire)
		}
	}
	for _, preserved := range []string{
		"keep env guidance", "keep auth guidance", "keep session guidance", "keep escaped guidance",
		"keep bracket auth guidance", "keep bracket API guidance",
		"keep short password guidance", "keep short API guidance", "keep short session guidance", "keep unterminated password guidance", "keep unterminated bearer guidance", "keep multiword guidance",
		"keep contextual short guidance", "keep short authorization guidance",
		"keep alphabetic password guidance", "keep alphabetic key guidance",
		"contact support", "keep digest guidance", "keep standalone digest guidance", "keep AWS guidance", "keep bearer guidance", "rotate it",
	} {
		if !strings.Contains(wire, preserved) {
			t.Fatalf("guidance %q was lost: %s", preserved, wire)
		}
	}
	if !strings.Contains(got.Message, "invalid token: signature mismatch") {
		t.Fatalf("natural token diagnostic was over-redacted: %q", got.Message)
	}
	if strings.Contains(got.Message, "![image](") || strings.Contains(got.Message, "[link](javascript:") || strings.Contains(got.Message, "[click][ref]") || strings.Contains(got.Message, "[ref]:") {
		t.Fatalf("active Markdown link/image survived: %q", got.Message)
	}
}

func TestUpstreamErrorBody_ClientSafeTextDoesNotDoubleSanitizeDetailsFold(t *testing.T) {
	body := `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"[click](javascript:alert(1)) password=\"main-secret-canary\"(javascript:alert(2)); keep guidance","errors":[{"message":"detail"}]}}`
	got := ParseUpstreamErrorBody(body)
	if !got.Recognized {
		t.Fatal("renderer envelope was not recognized")
	}
	text := got.ClientSafeText()
	if !strings.Contains(text, `［click］(javascript:alert(1))`) || strings.Contains(text, `[click]`) {
		t.Fatalf("source Markdown brackets were not neutralized: %q", text)
	}
	if !strings.Contains(text, redactedText) || strings.Contains(text, "[REDACTED](javascript:") {
		t.Fatalf("redaction marker became active Markdown: %q", text)
	}
	if strings.Contains(text, "main-secret-canary") {
		t.Fatalf("folded text leaked secret: %q", text)
	}
}

func TestParseUpstreamErrorBody_StripsCredentialBearingURLsFromEveryGuidanceField(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"status": "error",
		"error": map[string]any{
			"type":    "invalid-input",
			"code":    "invalid_input",
			"message": "see https://storage.test/object?X-Amz-Signature=message-url-secret-canary&X-Amz-Credential=credential and _https://storage.test/object?sig=prefixed-url-secret-canary",
			"url":     "https://signoz.io/docs/download/AKIA1234567890ABCDEF",
			"suggestions": []string{
				"inspect https://storage.test/object?sig=suggestion-url-secret-canary",
			},
			"errors": []map[string]any{{
				"message": "detail",
				"suggestions": []string{
					"open https://storage.test/object#eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJkZXRhaWwifQ.detail-url-secret-canary",
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := ParseUpstreamErrorBody(string(body))
	if !got.Recognized || got.URL != "" {
		t.Fatalf("unsafe renderer URL survived: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	for _, leaked := range []string{"message-url-secret-canary", "prefixed-url-secret-canary", "suggestion-url-secret-canary", "detail-url-secret-canary", "X-Amz-Credential", "AKIA1234567890ABCDEF"} {
		if strings.Contains(wire, leaked) {
			t.Fatalf("guidance URL leaked %q: %s", leaked, wire)
		}
	}
	if strings.Count(wire, "https：//storage.test/object") != 4 {
		t.Fatalf("safe, inert URL bases were not retained across guidance fields: %s", wire)
	}
}
