package docs

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

var markdownLinkRE = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

func ParseSitemapMarkdown(raw string) ([]SitemapEntry, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	type frame struct {
		indent int
		title  string
		slug   string
	}
	stack := make([]frame, 0)
	entries := make([]SitemapEntry, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		trimmedLeft := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmedLeft, "- ") && !strings.HasPrefix(trimmedLeft, "* ") {
			continue
		}
		indent := len(line) - len(trimmedLeft)
		body := strings.TrimSpace(trimmedLeft[2:])
		match := markdownLinkRE.FindStringSubmatch(body)
		var title, rawURL string
		if len(match) == 3 {
			title = strings.TrimSpace(match[1])
			rawURL = strings.TrimSpace(match[2])
		} else {
			title = strings.TrimSpace(strings.TrimPrefix(body, "-"))
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		slug := SlugifyHeading(title)
		if slug == "" {
			slug = "docs"
		}
		if rawURL == "" {
			stack = append(stack, frame{indent: indent, title: title, slug: slug})
			continue
		}
		canonical, ok := CanonicalDocURL(rawURL)
		if !ok {
			return nil, fmt.Errorf("line %d has out-of-scope docs URL %q", lineNo, rawURL)
		}
		parts := make([]string, 0, len(stack)+1)
		for _, f := range stack {
			if f.title != "" {
				parts = append(parts, f.title)
			}
		}
		parts = append(parts, title)
		sectionSlug := slug
		if len(stack) > 0 {
			sectionSlug = stack[0].slug
		}
		entries = append(entries, SitemapEntry{
			URL:               canonical,
			Title:             title,
			SectionSlug:       sectionSlug,
			SectionBreadcrumb: strings.Join(parts, " > "),
		})
		stack = append(stack, frame{indent: indent, title: title, slug: slug})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("sitemap contained no docs links")
	}
	return entries, nil
}
