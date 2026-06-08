// Package memlimit sets Go's soft memory limit (GOMEMLIMIT) from the container's
// cgroup memory limit at startup, so the garbage collector reclaims aggressively
// as the process approaches the limit instead of the cgroup OOM-killing the pod.
//
// It fails open: if no finite container limit can be determined (not Linux, no
// cgroup limit, unreadable) or GOMEMLIMIT was already set explicitly, it leaves
// the runtime default untouched and never errors. GOMEMLIMIT is a backstop for
// steady-state and gradual-growth pressure — it does not prevent a single
// transient allocation larger than the limit.
package memlimit

import (
	"context"
	"log/slog"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	// defaultRatio is the fraction of the container memory limit used as the soft
	// GOMEMLIMIT, leaving headroom for non-heap memory (goroutine stacks, the
	// mmap'd docs index, runtime overhead) so the GC target stays below the hard
	// cgroup limit.
	defaultRatio = 0.9

	// minLimitBytes guards against pathologically small or misread limits that
	// would cause GC thrashing; below this we leave GOMEMLIMIT unset.
	minLimitBytes = 64 << 20 // 64 MiB

	// v1UnlimitedThreshold: cgroup v1 reports "unlimited" as a near-max value
	// (e.g. 0x7FFFFFFFFFFFF000). Treat anything this large as no limit.
	v1UnlimitedThreshold = uint64(1) << 62

	cgroupV2Path = "/sys/fs/cgroup/memory.max"
	cgroupV1Path = "/sys/fs/cgroup/memory/memory.limit_in_bytes"

	ratioEnv = "SIGNOZ_GOMEMLIMIT_RATIO"
)

// Configure sets GOMEMLIMIT from the cgroup memory limit unless it was already
// set explicitly. It logs what it did (or why it skipped) and never errors.
func Configure(ctx context.Context, logger *slog.Logger) {
	// The Go runtime applies the GOMEMLIMIT env var before main runs, so a
	// non-default current value means the operator set it — respect it.
	if cur := debug.SetMemoryLimit(-1); cur != math.MaxInt64 {
		logger.InfoContext(ctx, "GOMEMLIMIT already set; leaving as-is",
			slog.Int64("gomemlimit_bytes", cur))
		return
	}

	limit, ok := readMemoryLimitFrom(cgroupV2Path, cgroupV1Path)
	if !ok {
		logger.InfoContext(ctx, "No cgroup memory limit detected; GOMEMLIMIT left unset (GC default)")
		return
	}
	if limit < minLimitBytes {
		logger.WarnContext(ctx, "cgroup memory limit too small; GOMEMLIMIT left unset",
			slog.Uint64("cgroup_limit_bytes", limit))
		return
	}

	ratio := ratioFromEnv(os.Getenv(ratioEnv))
	soft := int64(float64(limit) * ratio)
	debug.SetMemoryLimit(soft)
	logger.InfoContext(ctx, "GOMEMLIMIT set from cgroup memory limit",
		slog.Uint64("cgroup_limit_bytes", limit),
		slog.Float64("ratio", ratio),
		slog.Int64("gomemlimit_bytes", soft))
}

func ratioFromEnv(s string) float64 {
	if s == "" {
		return defaultRatio
	}
	r, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || r <= 0 || r > 1 {
		return defaultRatio
	}
	return r
}

// readMemoryLimitFrom returns the container memory limit in bytes from cgroup v2,
// then v1, or ok=false if neither yields a finite limit. It reads the standard
// root cgroup paths, which are correct for the common containerized case (private
// cgroup namespace); anything else fails open.
func readMemoryLimitFrom(v2Path, v1Path string) (uint64, bool) {
	// cgroup v2 takes precedence; if its file exists, don't consult v1.
	if b, err := os.ReadFile(v2Path); err == nil {
		return parseMemMax(string(b))
	}
	if b, err := os.ReadFile(v1Path); err == nil {
		return parseMemLimitV1(string(b))
	}
	return 0, false
}

// parseMemMax parses a cgroup v2 memory.max value; "max" means unlimited.
func parseMemMax(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return v, true
}

// parseMemLimitV1 parses cgroup v1 memory.limit_in_bytes; near-max values are the
// "unlimited" sentinel.
func parseMemLimitV1(s string) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil || v == 0 || v >= v1UnlimitedThreshold {
		return 0, false
	}
	return v, true
}
