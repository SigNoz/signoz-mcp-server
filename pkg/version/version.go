package version

import "strings"

const productName = "signoz-mcp-server"

// Version is overridden at build time via ldflags.
var Version = "dev"

// UserAgent returns the RFC 9110 product identifier for this build.
func UserAgent() string {
	value := strings.TrimSpace(Version)
	if value == "" {
		value = "dev"
	}
	return productName + "/" + value
}
