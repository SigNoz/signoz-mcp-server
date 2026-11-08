package tools

import "time"

// The generated response types use pointers for optional fields and time.Time
// for timestamps; these read them into the plain values the tool outputs carry.

func ptr[T any](v T) *T { return &v }

func deref[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}
	return *v
}

func derefMap[M ~map[string]string](v *M) map[string]string {
	if v == nil {
		return map[string]string{}
	}
	return *v
}

// timeString renders a timestamp the way SigNoz serialized it, so the tool
// output keeps the upstream representation.
func timeString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}
