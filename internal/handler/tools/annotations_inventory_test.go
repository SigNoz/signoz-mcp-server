package tools

import (
	"testing"
)

type annotationTriple struct {
	readOnly    bool
	destructive bool
	idempotent  bool
}

var (
	readTriple   = annotationTriple{readOnly: true, destructive: false, idempotent: true}
	createTriple = annotationTriple{readOnly: false, destructive: false, idempotent: false}
	updateTriple = annotationTriple{readOnly: false, destructive: true, idempotent: true}
	deleteTriple = annotationTriple{readOnly: false, destructive: true, idempotent: true}
)

// expectedToolAnnotations pins the advertised annotation triple for every
// registered tool. A new tool must be classified here (read/create/update/
// delete) before it can ship; see annotations.go for the class definitions.
var expectedToolAnnotations = map[string]annotationTriple{
	"signoz_aggregate_logs":              readTriple,
	"signoz_aggregate_traces":            readTriple,
	"signoz_check_metric_cardinality":    readTriple,
	"signoz_check_metric_usage":          readTriple,
	"signoz_execute_builder_query":       readTriple,
	"signoz_fetch_doc":                   readTriple,
	"signoz_get_alert":                   readTriple,
	"signoz_get_alert_history":           readTriple,
	"signoz_get_dashboard":               readTriple,
	"signoz_get_field_keys":              readTriple,
	"signoz_get_field_values":            readTriple,
	"signoz_get_notification_channel":    readTriple,
	"signoz_get_service_top_operations":  readTriple,
	"signoz_get_top_metrics":             readTriple,
	"signoz_get_trace_details":           readTriple,
	"signoz_get_view":                    readTriple,
	"signoz_list_alert_rules":            readTriple,
	"signoz_list_alerts":                 readTriple,
	"signoz_list_dashboard_templates":    readTriple,
	"signoz_list_dashboards":             readTriple,
	"signoz_list_metrics":                readTriple,
	"signoz_list_notification_channels":  readTriple,
	"signoz_list_services":               readTriple,
	"signoz_list_views":                  readTriple,
	"signoz_query_metrics":               readTriple,
	"signoz_search_docs":                 readTriple,
	"signoz_search_logs":                 readTriple,
	"signoz_search_traces":               readTriple,
	"signoz_create_alert":                createTriple,
	"signoz_create_dashboard":            createTriple,
	"signoz_create_notification_channel": createTriple,
	"signoz_create_view":                 createTriple,
	"signoz_import_dashboard":            createTriple,
	"signoz_update_alert":                updateTriple,
	"signoz_update_dashboard":            updateTriple,
	"signoz_update_notification_channel": updateTriple,
	"signoz_update_view":                 updateTriple,
	"signoz_delete_alert":                deleteTriple,
	"signoz_delete_dashboard":            deleteTriple,
	"signoz_delete_notification_channel": deleteTriple,
	"signoz_delete_view":                 deleteTriple,
}

func TestRegisteredToolAnnotationsMatchPinnedInventory(t *testing.T) {
	registered := registeredTestTools(t)
	if len(registered) == 0 {
		t.Fatal("no tools registered")
	}

	for name, entry := range registered {
		want, ok := expectedToolAnnotations[name]
		if !ok {
			t.Errorf("tool %s registered without a pinned annotation triple — classify it in expectedToolAnnotations and annotate it via a helper in annotations.go", name)
			continue
		}
		ann := entry.Tool.Annotations
		if ann.ReadOnlyHint == nil || ann.DestructiveHint == nil || ann.IdempotentHint == nil {
			t.Errorf("tool %s does not set the full annotation triple explicitly (readOnly=%v destructive=%v idempotent=%v)", name, ann.ReadOnlyHint, ann.DestructiveHint, ann.IdempotentHint)
			continue
		}
		got := annotationTriple{readOnly: *ann.ReadOnlyHint, destructive: *ann.DestructiveHint, idempotent: *ann.IdempotentHint}
		if got != want {
			t.Errorf("tool %s advertises annotation triple %+v, pinned %+v", name, got, want)
		}
	}

	for name := range expectedToolAnnotations {
		if _, ok := registered[name]; !ok {
			t.Errorf("pinned tool %s is no longer registered — remove it from expectedToolAnnotations", name)
		}
	}
}
