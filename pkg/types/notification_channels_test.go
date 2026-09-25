package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNotificationChannelConfig_AllProviderShapes(t *testing.T) {
	cases := []struct {
		kind string
		min  string
		full string
	}{
		{"slack", `{"apiUrl":"https://hooks.slack.test/x"}`, `{"sendResolved":false,"apiUrl":"https://hooks.slack.test/x","channel":"#ops","title":"title","text":"body","color":"danger","titleLink":"https://ops.test","pretext":"pre","fallback":"fb","footer":"SigNoz","fields":[{"title":"Service","value":"api","short":true}],"actions":[{"type":"button","text":"Runbook","url":"https://ops.test/runbook","style":"primary","confirm":{"text":"Open?","title":"Runbook","okText":"Yes","dismissText":"No"}},{"type":"button","text":"Ack","name":"ack","value":"1"}]}`},
		{"email", `{"to":"ops@example.test"}`, `{"sendResolved":false,"to":"ops@example.test","html":"<p>x</p>","headers":{"Subject":"Alert"}}`},
		{"webhook", `{"url":"https://example.test/hook"}`, `{"sendResolved":false,"url":"https://example.test/hook","username":"user","password":"secret","bearerToken":""}`},
		{"pagerduty", `{"routingKey":"key"}`, `{"sendResolved":false,"routingKey":"key","url":"https://events.test","source":"source","client":"client","clientUrl":"https://client.test","description":"desc","severity":"critical","component":"api","group":"prod","class":"service","details":{"team":"ops"}}`},
		{"opsgenie", `{"apiKey":"key"}`, `{"sendResolved":false,"apiKey":"key","apiUrl":"https://ops.test","message":"msg","description":"desc","source":"source","details":{"team":"ops"},"priority":"P1"}`},
		{"msteams", `{"webhookUrl":"https://teams.test/hook"}`, `{"sendResolved":false,"webhookUrl":"https://teams.test/hook","title":"title","text":"body"}`},
		{"googlechat", `{"webhookUrl":"https://chat.test/hook"}`, `{"sendResolved":false,"webhookUrl":"https://chat.test/hook","title":"title","text":"body"}`},
		{"jira", `{"site":"https://site.atlassian.net","project":"OPS","issueType":"Incident","email":"ops@example.test","apiToken":"token"}`, `{"sendResolved":false,"site":"https://site.atlassian.net","project":"OPS","issueType":"Incident","summary":"summary","description":"desc","priority":"High","labels":["alert"],"resolveTransition":"Done","reopenTransition":"Open","reopenDuration":"500ms","wontFixResolution":"Won't Fix","customFields":{"customfield_1":"x"},"email":"ops@example.test","apiToken":"token"}`},
		{"jsmops", `{"apiKey":"key"}`, `{"sendResolved":false,"apiKey":"key","message":"msg","description":"desc","priority":"P1","tags":"prod"}`},
		{"incidentio", `{"url":"https://incident.test","token":"token"}`, `{"sendResolved":false,"url":"https://incident.test","token":"token","title":"title","description":"desc","metadata":{"team":"ops"}}`},
	}
	for _, tc := range cases {
		for _, variant := range []struct {
			name string
			spec string
		}{
			{"sparse", tc.min},
			{"full", tc.full},
		} {
			t.Run(tc.kind+"/"+variant.name, func(t *testing.T) {
				input := `{"name":"channel","displayName":"Channel","config":{"kind":"` + tc.kind + `","spec":` + variant.spec + `}}`
				var create NotificationChannelCreate
				if err := json.Unmarshal([]byte(input), &create); err != nil {
					t.Fatalf("decode: %v", err)
				}
				encoded, err := json.Marshal(create)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(encoded), `"kind":"`+tc.kind+`"`) {
					t.Fatalf("round trip lost kind: %s", encoded)
				}
				if variant.name == "sparse" && strings.Contains(string(encoded), "sendResolved") {
					t.Fatalf("omitted sendResolved gained a default: %s", encoded)
				}
				if variant.name == "full" && !strings.Contains(string(encoded), `"sendResolved":false`) {
					t.Fatalf("explicit false was lost: %s", encoded)
				}
				var update NotificationChannelUpdate
				updateInput := `{"config":{"kind":"` + tc.kind + `","spec":` + variant.spec + `}}`
				if err := json.Unmarshal([]byte(updateInput), &update); err != nil {
					t.Fatalf("full replacement decode: %v", err)
				}
			})
		}
	}
}

func TestNotificationChannelSlackSpec_RejectsIncompleteAttachments(t *testing.T) {
	for want, spec := range map[string]string{
		"fields[0] requires title and value": `"fields":[{"title":"Service"}]`,
		"actions[0] requires type and text":  `"actions":[{"type":"button","url":"https://ops.test"}]`,
		"actions[0] requires url or name":    `"actions":[{"type":"button","text":"Open"}]`,
		"actions[0].confirm requires text":   `"actions":[{"type":"button","text":"Open","url":"https://ops.test","confirm":{"title":"Sure?"}}]`,
		"color cannot be empty":              `"color":" "`,
	} {
		t.Run(want, func(t *testing.T) {
			input := `{"name":"channel","config":{"kind":"slack","spec":{"apiUrl":"https://hooks.slack.test/x",` + spec + `}}}`
			var create NotificationChannelCreate
			err := json.Unmarshal([]byte(input), &create)
			if err == nil {
				err = create.Validate()
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %q", err, want)
			}
		})
	}
}

func TestNotificationChannelConfig_StrictUnknownFields(t *testing.T) {
	for _, input := range []string{
		`{"name":"channel","config":{"kind":"webhook","spec":{"url":"https://example.test","extra":true}}}`,
		`{"name":"channel","config":{"kind":"webhook","spec":{"url":"https://example.test"},"extra":true}}`,
		`{"name":"channel","config":{"kind":"webhook","spec":{"url":"https://example.test"}},"type":"webhook"}`,
	} {
		var create NotificationChannelCreate
		if err := json.Unmarshal([]byte(input), &create); err == nil {
			t.Fatalf("unknown field accepted: %s", input)
		}
	}
}

func TestNotificationChannelCreateIdentityModes(t *testing.T) {
	config := NotificationChannelConfig{Kind: "webhook", Spec: &NotificationChannelWebhookSpec{URL: "https://example.test"}}
	for _, tc := range []struct {
		name string
		in   NotificationChannelCreate
		ok   bool
	}{
		{"explicit name", NotificationChannelCreate{Name: "channel", Config: config, DisplayName: "Channel"}, true},
		{"generated name", NotificationChannelCreate{GenerateName: true, DisplayName: "Channel", Config: config}, true},
		{"both identities", NotificationChannelCreate{Name: "channel", GenerateName: true, DisplayName: "Channel", Config: config}, false},
		{"neither identity", NotificationChannelCreate{Config: config}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() error = %v, ok=%v", err, tc.ok)
			}
		})
	}
}

func TestValidateCanonicalPrometheusDuration(t *testing.T) {
	for _, valid := range []string{"500ms", "1s", "1m30s", "3d"} {
		if err := ValidateCanonicalPrometheusDuration(valid); err != nil {
			t.Errorf("%q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "72h", "1s500ms500ms", "500sm"} {
		if err := ValidateCanonicalPrometheusDuration(invalid); err == nil {
			t.Errorf("%q accepted", invalid)
		}
	}
}

func TestNotificationChannelListParamsNormalize(t *testing.T) {
	params := NotificationChannelListParams{Limit: 500}
	if err := params.Normalize(); err != nil {
		t.Fatal(err)
	}
	if params.Limit != 200 || params.Sort != "updated_at" || params.Order != "desc" {
		t.Fatalf("normalized params = %#v", params)
	}
	for _, params := range []NotificationChannelListParams{{Limit: -1}, {Offset: -1}, {Kind: "unknown"}, {Sort: "unknown"}, {Order: "unknown"}} {
		if err := params.Normalize(); err == nil {
			t.Fatalf("invalid params accepted: %#v", params)
		}
	}
}
