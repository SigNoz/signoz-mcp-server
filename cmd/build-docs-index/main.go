package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/docs"
)

const (
	corpusPath               = "internal/docs/assets/corpus.gob.gz"
	manifestPath             = "internal/docs/assets/corpus.manifest.json"
	fallbackCorpusPath       = "internal/docs/testdata/corpus_fallback.gob.gz"
	fallbackManifestPath     = "internal/docs/testdata/corpus_fallback.manifest.json"
	maxBuildTimeoutSitemap   = 20 * time.Second
	maxBuildTimeoutAggregate = 20 * time.Minute
)

// Flags:
//
//	-allow-fallback: when a live sitemap fetch fails, write the minimal
//	                 testdata fallback corpus instead of exiting non-zero.
//	                 Intended for local dev where the host has no network.
//	                 CI / release builds should omit this so a failed
//	                 refresh fails loudly instead of committing stale assets.
//	-fallback-only: regenerate only the testdata fallback blob; do not
//	                touch the embedded live corpus.
func main() {
	allowFallback := flag.Bool("allow-fallback", false, "on live sitemap failure, write the testdata fallback corpus instead of exiting non-zero")
	fallbackOnly := flag.Bool("fallback-only", false, "only regenerate the testdata fallback corpus; skip the live build")
	flag.Parse()

	if *fallbackOnly {
		if err := writeFallbackCorpus(); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote fallback %s and %s\n", fallbackCorpusPath, fallbackManifestPath)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), maxBuildTimeoutAggregate)
	defer cancel()

	fetcher := docs.NewFetcher(docs.FetcherConfig{Timeout: maxBuildTimeoutSitemap})
	sitemap := fetcher.Fetch(ctx, docs.DefaultSitemapURL)
	if sitemap.Status != docs.FetchStatusOK {
		if *allowFallback {
			log.Printf("WARN: live sitemap fetch failed (%v); -allow-fallback is set, writing testdata fallback corpus", sitemap.Err)
			if err := writeFallbackCorpus(); err != nil {
				log.Fatal(err)
			}
			return
		}
		log.Fatalf("live sitemap fetch failed: %v (pass -allow-fallback to write the testdata fallback instead)", sitemap.Err)
	}
	entries, err := docs.ParseSitemapMarkdown(sitemap.Body)
	if err != nil {
		log.Fatalf("parse sitemap: %v", err)
	}

	type fetchOutcome struct {
		entry  docs.SitemapEntry
		result docs.PageFetch
	}
	outcomes := make([]fetchOutcome, len(entries))
	var wg sync.WaitGroup
	wg.Add(len(entries))
	start := time.Now()
	for i, entry := range entries {
		go func(i int, entry docs.SitemapEntry) {
			defer wg.Done()
			outcomes[i] = fetchOutcome{entry: entry, result: fetcher.Fetch(ctx, entry.URL)}
		}(i, entry)
	}
	wg.Wait()
	log.Printf("fetched %d pages in %s", len(entries), time.Since(start).Round(time.Millisecond))

	pages := make([]docs.PageRecord, 0, len(entries))
	failures := 0
	for _, o := range outcomes {
		if o.result.Status == docs.FetchStatusNotFound {
			log.Printf("WARN: skipping 404 docs page %s", o.entry.URL)
			continue
		}
		if o.result.Status != docs.FetchStatusOK {
			log.Printf("WARN: failed to fetch docs page %s: %v", o.entry.URL, o.result.Err)
			failures++
			continue
		}
		pages = append(pages, pageFromFetch(o.entry, o.result))
	}
	if len(entries) > 0 && failures*100 > len(entries)*10 {
		log.Fatalf("too many docs fetch failures: %d/%d", failures, len(entries))
	}
	snapshot := docs.CorpusSnapshot{
		SchemaVersion: docs.CorpusSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		SitemapRaw:    sitemap.Body,
		SitemapHash:   docs.SitemapHash(sitemap.Body),
		Pages:         pages,
	}
	if err := writeCorpusAndManifest(corpusPath, manifestPath, snapshot); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s and %s (%d pages)\n", corpusPath, manifestPath, len(pages))
}

func pageFromFetch(entry docs.SitemapEntry, result docs.PageFetch) docs.PageRecord {
	body := result.Body
	return docs.PageRecord{
		URL:               entry.URL,
		Title:             docs.FirstHeadingTitle(body, entry.Title),
		SectionSlug:       entry.SectionSlug,
		SectionBreadcrumb: entry.SectionBreadcrumb,
		HeadingsJSON:      jsonString(docs.ExtractHeadings(body)),
		BodyMarkdown:      body,
		FetchedAt:         result.FetchedAt,
		SourceETag:        result.ETag,
	}
}

func writeFallbackCorpus() error {
	now := time.Now().UTC()
	pages := []docs.PageRecord{
		{
			URL:               "https://signoz.io/docs/logs-management/overview/",
			Title:             "Logs Management Overview",
			SectionSlug:       "logs-management",
			SectionBreadcrumb: "Logs Management > Overview",
			BodyMarkdown:      "# Logs Management Overview\n\nSigNoz lets you collect, search, filter, and analyze logs with OpenTelemetry.\n",
			FetchedAt:         now,
		},
		{
			URL:               "https://signoz.io/docs/install/docker/",
			Title:             "Install SigNoz Using Docker",
			SectionSlug:       "install",
			SectionBreadcrumb: "Install > Docker",
			BodyMarkdown:      "# Install SigNoz Using Docker\n\nUse Docker Compose to run SigNoz locally for development and evaluation.\n",
			FetchedAt:         now,
		},
	}
	for i := range pages {
		pages[i].HeadingsJSON = jsonString(docs.ExtractHeadings(pages[i].BodyMarkdown))
	}
	sitemapRaw := "- [Logs Management Overview](https://signoz.io/docs/logs-management/overview/)\n- [Install SigNoz Using Docker](https://signoz.io/docs/install/docker/)\n"
	snapshot := docs.CorpusSnapshot{
		SchemaVersion: docs.CorpusSchemaVersion,
		BuiltAt:       now,
		SitemapRaw:    sitemapRaw,
		SitemapHash:   docs.SitemapHash(sitemapRaw),
		Pages:         pages,
	}
	return writeCorpusAndManifest(fallbackCorpusPath, fallbackManifestPath, snapshot)
}

func writeCorpusAndManifest(corpusFile, manifestFile string, snapshot docs.CorpusSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(corpusFile), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifestFile), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := docs.EncodeCorpus(&buf, snapshot); err != nil {
		return err
	}
	if err := os.WriteFile(corpusFile, buf.Bytes(), 0o644); err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(docs.ManifestForSnapshot(snapshot), "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')
	return os.WriteFile(manifestFile, manifest, 0o644)
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
