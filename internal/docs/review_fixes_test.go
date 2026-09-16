package docs

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCloseWaitsForInFlightApplyDelta(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries := manyEntries(5)
	bodies := bodiesForEntries(entries)
	snapshot := snapshotForEntries(entries, bodies)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	handle := reg.currentHandle(t)

	gate := make(chan struct{})
	entered := make(chan struct{})
	applyDeltaBeforePublish = func() {
		close(entered)
		<-gate
	}
	t.Cleanup(func() { applyDeltaBeforePublish = nil })

	bodies[entries[0].URL] = "# Page 00\n\nRewritten.\n"
	next := snapshotForEntries(entries, bodies)
	delta := diffSnapshots(snapshot, next)
	require.Len(t, delta.changed, 1)

	applyErr := make(chan error, 1)
	go func() { applyErr <- reg.ApplyDelta(ctx, next, delta) }()
	<-entered

	closed := make(chan struct{})
	go func() {
		reg.Close(context.Background())
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a writer was mid-apply")
	case <-time.After(50 * time.Millisecond):
	}

	close(gate)
	require.NoError(t, <-applyErr)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not complete after the writer finished")
	}
	require.Eventually(t, func() bool { return atomic.LoadInt64(&handle.entries) == 0 }, time.Second, 5*time.Millisecond)
	require.False(t, reg.Ready())
	require.ErrorContains(t, reg.Swap(ctx, snapshot), "closed")
	require.ErrorContains(t, reg.ApplyDelta(ctx, next, delta), "closed")
}

func TestNewRefresherKeepsFullIntervalWhenIncrementalDisabled(t *testing.T) {
	reg := newTestRegistry(t, testSnapshot())
	refresher := NewRefresher(slog.Default(), reg, nil, RefreshConfig{
		DisableScheduled:    true,
		FullRefreshInterval: time.Hour,
	})
	require.Equal(t, time.Hour, refresher.cfg.FullRefreshInterval,
		"a disabled incremental schedule must not trip the ordering fallback")

	refresher = NewRefresher(slog.Default(), reg, nil, RefreshConfig{
		RefreshInterval:     6 * time.Hour,
		FullRefreshInterval: time.Hour,
	})
	require.Equal(t, defaultFullRefreshInterval, refresher.cfg.FullRefreshInterval,
		"with both schedules enabled the ordering fallback still applies")
}
