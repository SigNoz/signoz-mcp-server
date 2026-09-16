package docs

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/guardrails"
	"github.com/stretchr/testify/require"
)

// TestGuardrail_DocsIndexBuildPeakHeap builds the embedded corpus while
// sampling the heap and fails when the peak or the resident index outgrow the
// reviewed budgets in guardrails/policy.go. A single-batch build of this corpus
// peaks near 325 MiB and OOM-killed 512Mi containers; the chunked build stays
// well under the budget.
func TestGuardrail_DocsIndexBuildPeakHeap(t *testing.T) {
	require.LessOrEqual(t, indexBatchSize, guardrails.MaxDocsIndexBatchSize,
		"indexBatchSize must stay bounded; the batch is what drives the build peak")

	snapshot, err := LoadEmbeddedCorpus()
	require.NoError(t, err)
	require.Greater(t, len(snapshot.Pages), 500, "guardrail assumes the full embedded corpus")

	baseline := settledHeapInuse()
	var stop atomic.Bool
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for !stop.Load() {
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			time.Sleep(500 * time.Microsecond)
		}
	}()

	idx, err := BuildIndex(snapshot)
	require.NoError(t, err)
	stop.Store(true)
	<-done
	resident := settledHeapInuse() - baseline
	require.NoError(t, idx.Close())

	peakOverBaseline := peak.Load() - baseline
	t.Logf("pages=%d peak_over_baseline=%d MiB resident=%d MiB budget_peak=%d MiB budget_resident=%d MiB",
		len(snapshot.Pages), peakOverBaseline>>20, resident>>20,
		guardrails.DocsIndexBuildPeakHeapBudgetBytes>>20, guardrails.DocsIndexResidentBudgetBytes>>20)
	require.LessOrEqual(t, peakOverBaseline, uint64(guardrails.DocsIndexBuildPeakHeapBudgetBytes),
		"docs index build peak heap exceeded the guardrail budget; check indexBatchSize and BuildIndex chunking")
	require.LessOrEqual(t, resident, uint64(guardrails.DocsIndexResidentBudgetBytes),
		"docs index resident heap exceeded the guardrail budget")
}

func settledHeapInuse() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}
