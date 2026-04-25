package gen

import "testing"

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"GetChannelByID":   "get_channel_by_id",
		"ListMetrics":      "list_metrics",
		"listMetrics":      "list_metrics",
		"URLPath":          "url_path",
		"ID":               "id",
		"SAMLLogin":        "saml_login",
		"CreateOrg":        "create_org",
		"":                 "",
		"CreateHTTPRoute":  "create_http_route",
		"OAuthCallback":    "o_auth_callback", // acronym boundary limitation
	}
	for in, want := range cases {
		if got := snakeCase(in); got != want {
			t.Errorf("snakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoIdent(t *testing.T) {
	cases := map[string]string{
		"id":              "Id",
		"cloud-provider":  "CloudProvider",
		"service_id":      "ServiceId",
		"my.field.name":   "MyFieldName",
		"":                "Unnamed",
	}
	for in, want := range cases {
		if got := goIdent(in); got != want {
			t.Errorf("goIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToolName(t *testing.T) {
	if got, want := toolName("GetChannelByID"), "signoz_get_channel_by_id"; got != want {
		t.Errorf("toolName = %q, want %q", got, want)
	}
}
