package paginate

import "testing"

// TestParseParamsClamped pins the uniform per-page clamp for the summary list
// tools, accepting number-or-string limits.
func TestParseParamsClamped(t *testing.T) {
	limit, _, clamped := ParseParamsClamped(map[string]any{"limit": "999999"})
	if limit != MaxLimit || !clamped {
		t.Fatalf("over-cap: limit=%d clamped=%v, want %d true", limit, clamped, MaxLimit)
	}

	limit, _, clamped = ParseParamsClamped(map[string]any{"limit": float64(10)})
	if limit != 10 || clamped {
		t.Fatalf("number under-cap: limit=%d clamped=%v, want 10 false", limit, clamped)
	}

	limit, offset, clamped := ParseParamsClamped(map[string]any{})
	if limit != DefaultLimit || offset != DefaultOffset || clamped {
		t.Fatalf("defaults: limit=%d offset=%d clamped=%v", limit, offset, clamped)
	}
}
