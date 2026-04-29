package docs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestIndexSearchFetchAndSwap(t *testing.T) {
	// bleve's AnalysisQueue starts long-lived worker goroutines that don't
	// exit when an Index is closed (library-owned global). They are not a
	// real leak in our code, so filter them out of the goleak check.
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg, err := NewIndexRegistry(ctx, testSnapshot())
	require.NoError(t, err)
	defer reg.Close(context.Background())

	search, err := reg.Search(ctx, "docker logs", "", 3)
	require.NoError(t, err)
	require.NotEmpty(t, search.Results)

	filtered, err := reg.Search(ctx, "docker", "install", 3)
	require.NoError(t, err)
	require.NotEmpty(t, filtered.Results)
	require.Equal(t, "install", filtered.Results[0].SectionSlug)

	doc, code, err := reg.FetchDoc(ctx, "/docs/install/docker/", "prerequisites")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Equal(t, "prerequisites", doc.Heading)
	require.Contains(t, doc.Content, "Docker")

	_, code, err = reg.FetchDoc(ctx, "https://example.com/docs/install/docker/", "")
	require.NoError(t, err)
	require.Equal(t, CodeOutOfScopeURL, code)

	err = reg.Swap(ctx, testSnapshot())
	require.NoError(t, err)
}

func TestTruncateContentIsUTF8Safe(t *testing.T) {
	content := strings.Repeat("日志🙂", 80*1024)
	truncated, reason := truncateContent(content, 256*1024)
	require.Equal(t, "size", reason)
	require.True(t, strings.HasPrefix(content, truncated))
}

func testSnapshot() CorpusSnapshot {
	now := time.Now().UTC()
	logs := "# Logs Management Overview\n\nSigNoz can receive Docker logs and OpenTelemetry logs.\n"
	install := "# Install SigNoz Using Docker\n\n## Prerequisites\n\nDocker and Docker Compose are required.\n\n## Start\n\nRun docker compose up.\n"
	return CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       now,
		SitemapRaw:    "- [Logs Management Overview](https://signoz.io/docs/logs-management/overview/)\n- [Install SigNoz Using Docker](https://signoz.io/docs/install/docker/)\n",
		SitemapHash:   "test",
		Pages: []PageRecord{
			{
				URL:               "https://signoz.io/docs/logs-management/overview/",
				Title:             "Logs Management Overview",
				SectionSlug:       "logs-management",
				SectionBreadcrumb: "Logs Management > Overview",
				HeadingsJSON:      mustJSON(ExtractHeadings(logs)),
				BodyMarkdown:      logs,
				FetchedAt:         now,
			},
			{
				URL:               "https://signoz.io/docs/install/docker/",
				Title:             "Install SigNoz Using Docker",
				SectionSlug:       "install",
				SectionBreadcrumb: "Install > Docker",
				HeadingsJSON:      mustJSON(ExtractHeadings(install)),
				BodyMarkdown:      install,
				FetchedAt:         now,
			},
		},
	}
}
