package tools

import (
	"reflect"
	"sort"
	"testing"
)

// TestAggregationDescriptionDriftGuard pins the aggregation set named in the
// validation-error guidance (allowedAggregations) against the set the parser
// accepts (validAggregations). The error text is a recovery path an agent acts
// on, so the two sets drifting apart would send agents retrying with values we
// reject, or hide values we accept. The registered tool descriptions are pinned
// separately by the wire catalog.
//
// TODO(live-backend): the authoritative set is the SigNoz query-builder
// backend's accepted operators; an e2e check should diff validAggregations
// against a real instance.
func TestAggregationDescriptionDriftGuard(t *testing.T) {
	// Derive the documented set from allowedAggregations.
	documented := splitCSV(allowedAggregations)
	sort.Strings(documented)

	inCode := make([]string, 0, len(validAggregations))
	for k := range validAggregations {
		inCode = append(inCode, k)
	}
	sort.Strings(inCode)

	if !reflect.DeepEqual(documented, inCode) {
		t.Fatalf("aggregation set drift: description advertises %v but validAggregations accepts %v; keep allowedAggregations and validAggregations in sync", documented, inCode)
	}
}

// liveBackendDriftCheckSkipped is the shared skip hook for the live-backend
// drift checks referenced by the TODO(live-backend) notes above. A real
// implementation would hit a live SigNoz instance; until that is wired up (and
// gated behind credentials), the check is skipped so unit runs stay hermetic.
func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = appendTrimmed(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = appendTrimmed(out, cur)
	return out
}

func appendTrimmed(out []string, s string) []string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	if start == end {
		return out
	}
	return append(out, s[start:end])
}
