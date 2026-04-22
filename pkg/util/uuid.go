package util

import "regexp"

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// IsUUIDv7 reports whether s is a canonical UUID version 7 string.
// SigNoz's v2 endpoints require UUIDv7 on path params and reject anything else
// with invalid_input (HTTP 400), so we check client-side to produce a clearer
// error before the round-trip.
func IsUUIDv7(s string) bool {
	return uuidV7Pattern.MatchString(s)
}
