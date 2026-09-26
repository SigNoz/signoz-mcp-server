package timeutil

import (
	"math"
	"math/big"
	"strconv"
	"testing"
)

// Mixed epoch units and large values must not silently move a user's query window.
func FuzzExplicitTimestampUnits(f *testing.F) {
	for _, raw := range []int64{math.MinInt64, -1, 0, 1, 1700000000, 1700000000123,
		1700000000123456, 1700000000123456789, math.MaxInt64} {
		f.Add(raw)
	}
	for _, boundary := range []int64{1e11, 1e14, 1e17, math.MaxInt64 / 1000000000, math.MaxInt64 / 1000000, math.MaxInt64 / 1000} {
		for _, delta := range []int64{-1, 0, 1} {
			f.Add(boundary + delta)
		}
	}
	f.Fuzz(func(t *testing.T, raw int64) {
		value := strconv.FormatInt(raw, 10)
		for _, unit := range []string{UnitMillis, UnitNanos} {
			start, end := GetTimestampsWithDefaults(map[string]any{
				"start": value, "end": value, "timeRange": "1h",
			}, unit)
			want := big.NewInt(raw)
			if raw > 0 {
				// Decimal digit bands are the documented epoch-unit contract.
				scale := int64(1)
				for digits := len(value); digits <= 17; digits += 3 {
					scale *= 1000
					if scale == 1e9 {
						break
					}
				}
				want.Mul(want, big.NewInt(scale))
				if unit == UnitMillis {
					want.Quo(want, big.NewInt(1e6))
				}
				if want.Cmp(big.NewInt(math.MaxInt64)) > 0 {
					want.SetInt64(math.MaxInt64)
				}
			}
			if start != want.String() || end != want.String() {
				t.Fatalf("epoch %d to %s: got (%s, %s), want %s", raw, unit, start, end, want)
			}
		}
	})
}
