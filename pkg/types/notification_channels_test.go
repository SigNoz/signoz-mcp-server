package types

import (
	"testing"
)

func TestNotificationChannelListParamsNormalize(t *testing.T) {
	params := NotificationChannelListParams{Limit: 500}
	if err := params.Normalize(); err != nil {
		t.Fatal(err)
	}
	if params.Limit != 200 || params.Sort != "updated_at" || params.Order != "desc" {
		t.Fatalf("normalized params = %#v", params)
	}
	for _, params := range []NotificationChannelListParams{{Limit: -1}, {Offset: -1}, {Sort: "unknown"}, {Order: "unknown"}} {
		if err := params.Normalize(); err == nil {
			t.Fatalf("invalid params accepted: %#v", params)
		}
	}
}
