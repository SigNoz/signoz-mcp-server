package types

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// TestAlertRuleFieldsExistUpstream pins the hand-written alert schema structs
// against the generated SigNoz types: every wire-bound json tag we declare
// must exist on the generated counterpart, so a renamed or removed upstream
// field fails here instead of producing rejected writes. DRIFT: fails when
// `make gen` moves the spec and a field this schema teaches no longer exists.
// The reverse direction is deliberate slack: upstream may add fields before we
// teach them.
func TestAlertRuleFieldsExistUpstream(t *testing.T) {
	pairs := []struct {
		name      string
		ours      reflect.Type
		generated reflect.Type
	}{
		{"AlertRule", reflect.TypeOf(AlertRule{}), reflect.TypeOf(apitypes.RuletypesPostableRule{})},
		{"AlertCondition", reflect.TypeOf(AlertCondition{}), reflect.TypeOf(apitypes.RuletypesRuleCondition{})},
	}
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			upstream := jsonTagSet(pair.generated)
			for _, tag := range jsonTags(pair.ours) {
				if _, ok := upstream[tag]; !ok {
					t.Errorf("%s declares %q, which the generated %s does not have; the field was renamed or removed upstream, or never existed",
						pair.name, tag, pair.generated.Name())
				}
			}
		})
	}
}

// TestAlertTypeDescriptionNamesEveryUpstreamValue pins the alertType teaching
// against the generated enum: a value SigNoz adds must appear in the schema
// description, or agents are taught a stale closed set (the ai_observability
// source bug, nerve-pod#400, in alert form).
func TestAlertTypeDescriptionNamesEveryUpstreamValue(t *testing.T) {
	field, ok := reflect.TypeOf(AlertRule{}).FieldByName("AlertType")
	if !ok {
		t.Fatal("AlertRule has no AlertType field")
	}
	description := field.Tag.Get("jsonschema")
	for _, value := range []apitypes.RuletypesAlertType{
		apitypes.RuletypesAlertTypeMETRICBASEDALERT,
		apitypes.RuletypesAlertTypeLOGSBASEDALERT,
		apitypes.RuletypesAlertTypeTRACESBASEDALERT,
		apitypes.RuletypesAlertTypeEXCEPTIONSBASEDALERT,
		apitypes.RuletypesAlertTypeAITRACESBASEDALERT,
	} {
		if !value.Valid() {
			t.Fatalf("test value %q is not in the generated enum; regenerate this list from zz_generated_ruletypes_alert_type.go", value)
		}
		if !strings.Contains(description, string(value)) {
			t.Errorf("alertType description does not name upstream value %q; teach it or make the set open", value)
		}
	}
}

func jsonTags(t reflect.Type) []string {
	var tags []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous {
			tags = append(tags, jsonTags(field.Type)...)
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func jsonTagSet(t reflect.Type) map[string]struct{} {
	set := make(map[string]struct{})
	for _, tag := range jsonTags(t) {
		set[tag] = struct{}{}
	}
	return set
}
