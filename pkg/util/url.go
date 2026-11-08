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
	if parsed.User != nil {
		return "", fmt.Errorf("URL must not include user info")
	}

	rawHost := parsed.Hostname()
	if rawHost == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	// Lowercasing and the IDNA mapping net/http dials with disagree on some
	// letters, so a non-ASCII host could compare equal to one host and connect
	// to another. Callers must send the punycode form.
	for i := 0; i < len(rawHost); i++ {
		if rawHost[i] >= 0x80 {
			return "", fmt.Errorf("host must be ASCII; use the punycode form")
		}
	}
	host := strings.ToLower(rawHost)
	if host == "localhost" || host == "0.0.0.0" || host == "::" {
		return "", fmt.Errorf("host %q is not allowed", host)
	}

	return originOf(parsed), nil
}

// originOf builds scheme://host[:port] from a parsed URL: lowercased, with a
// default port dropped and path, query and fragment ignored. Empty when the
// URL has no host.
func originOf(parsed *url.URL) string {
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}

	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}

	origin := scheme + "://" + host
	if port != "" {
		origin += ":" + port
	}
	return origin
}

// SameSigNozOrigin reports whether two SigNoz URLs name the same origin. The
// configured SIGNOZ_URL may carry a path, so only scheme, host and port count.
func SameSigNozOrigin(a, b string) bool {
	parsedA, errA := url.Parse(strings.TrimSpace(a))
	parsedB, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil {
		return false
	}
	originA := originOf(parsedA)
	return originA != "" && originA == originOf(parsedB)
}

// NormalizeSigNozURLFormInput normalizes URLs entered by humans in auth forms. It
// accepts protocol-less hosts by assuming HTTPS and strips path/query/fragment
// suffixes before delegating to the strict origin validator.
func NormalizeSigNozURLFormInput(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("URL is required")
	}

	normalized := trimmed
	if strings.HasPrefix(normalized, "//") {
		if hasHTTPishPrefix(strings.TrimPrefix(normalized, "//")) {
			return "", fmt.Errorf("malformed URL: http and https URLs must start with // after the scheme")
		}
		normalized = "https:" + normalized
	} else if hasMalformedHTTPScheme(normalized) {
		return "", fmt.Errorf("malformed URL: http and https URLs must start with // after the scheme")
	} else if !hasExplicitScheme(normalized) {
		normalized = "https://" + normalized
	}

	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("malformed URL: %w", err)
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""

	return NormalizeSigNozURL(parsed.String())
}

func hasExplicitScheme(rawURL string) bool {
	schemeSep := strings.Index(rawURL, "://")
	if schemeSep < 0 {
		return false
	}

	pathStart := strings.IndexAny(rawURL, "/?#")
	return pathStart < 0 || schemeSep < pathStart
}

func hasMalformedHTTPScheme(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return hasHTTPishPrefix(lower) && !strings.HasPrefix(lower, "http://") &&
		!strings.HasPrefix(lower, "https://")
}

func hasHTTPishPrefix(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	return strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:")
}
