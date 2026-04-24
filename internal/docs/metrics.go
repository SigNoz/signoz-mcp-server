package docs

import (
	"context"
	"time"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
)

// RecordMetrics emits the active index gauges from the registry's current
// snapshot. It returns false when there is no active index to report.
func (r *IndexRegistry) RecordMetrics(ctx context.Context, meters *otelpkg.Meters) bool {
	if meters == nil {
		return false
	}
	entry, release, ok := r.acquire()
	if !ok {
		return false
	}
	defer release()

	snapshot := entry.snapshot
	age := time.Since(snapshot.BuiltAt)
	if age < 0 {
		age = 0
	}
	meters.DocsIndexAge.Record(ctx, age.Seconds())
	meters.DocsIndexSizeBytes.Record(ctx, approximateCorpusSizeBytes(snapshot))
	meters.DocsIndexDocCount.Record(ctx, int64(len(snapshot.Pages)))
	meters.DocsIndexGeneration.Record(ctx, int64(entry.generation))
	return true
}

func approximateCorpusSizeBytes(snapshot CorpusSnapshot) int64 {
	var total int64
	total += int64(len(snapshot.SitemapRaw) + len(snapshot.SitemapHash))
	for _, page := range snapshot.Pages {
		total += int64(len(page.URL))
		total += int64(len(page.Title))
		total += int64(len(page.SectionSlug))
		total += int64(len(page.SectionBreadcrumb))
		total += int64(len(page.HeadingsJSON))
		total += int64(len(page.BodyMarkdown))
		total += int64(len(page.SourceETag))
	}
	return total
}
