package docs

import (
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9\-]+`)

func CanonicalDocURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "/docs/") {
		raw = "https://signoz.io" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "signoz.io") {
		return "", false
	}
	cleanPath := path.Clean(u.Path)
	if cleanPath == "." || cleanPath == "/" {
		cleanPath = "/"
	}
	if !strings.HasPrefix(cleanPath, "/docs/") {
		return "", false
	}
	if !strings.HasSuffix(cleanPath, "/") && path.Ext(cleanPath) == "" {
		cleanPath += "/"
	}
	u.Path = cleanPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), true
}

func IsDocURL(raw string) bool {
	_, ok := CanonicalDocURL(raw)
	return ok
}

func SlugifyHeading(text string) string {
	text = strings.TrimSpace(strings.TrimLeft(text, "#"))
	text = strings.ToLower(text)
	var b strings.Builder
	lastDash := false
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	s = nonSlugChars.ReplaceAllString(s, "")
	return s
}

func ExtractHeadings(markdown string) []Heading {
	lines := strings.Split(markdown, "\n")
	seen := map[string]int{}
	headings := make([]Heading, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "#######") {
			continue
		}
		level := 0
		for _, r := range trimmed {
			if r != '#' {
				break
			}
			level++
		}
		if level == 0 || level > 6 || len(trimmed) <= level || trimmed[level] != ' ' {
			continue
		}
		text := strings.TrimSpace(trimmed[level:])
		id := SlugifyHeading(text)
		if id == "" {
			continue
		}
		seen[id]++
		if seen[id] > 1 {
			id = id + "-" + strconv.Itoa(seen[id])
		}
		headings = append(headings, Heading{ID: id, Text: text, Level: level})
	}
	return headings
}

func NormalizeHeadingID(input string) string {
	input = strings.TrimSpace(input)
	input = strings.TrimPrefix(input, "#")
	input = strings.TrimSpace(strings.TrimLeft(input, "#"))
	return SlugifyHeading(input)
}

func FirstHeadingTitle(markdown, fallback string) string {
	for _, h := range ExtractHeadings(markdown) {
		if h.Level == 1 {
			return h.Text
		}
	}
	if fallback != "" {
		return fallback
	}
	return "Untitled"
}
