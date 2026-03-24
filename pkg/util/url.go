package util

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeSigNozURL validates that rawURL is safe to use as a SigNoz backend
// target and returns the canonical origin form (scheme://host[:port]).
// It only allows origin URLs and strips default ports for stable cache keys.
func NormalizeSigNozURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("malformed URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme %q not allowed, must be http or https", parsed.Scheme)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without a path")
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without query parameters")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without a fragment")
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	if host == "localhost" || host == "0.0.0.0" || host == "::" {
		return "", fmt.Errorf("host %q is not allowed", host)
	}

	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	originHost := host
	if strings.Contains(originHost, ":") && !strings.HasPrefix(originHost, "[") {
		originHost = "[" + originHost + "]"
	}

	origin := scheme + "://" + originHost
	if port != "" {
		origin += ":" + port
	}
	return origin, nil
}
