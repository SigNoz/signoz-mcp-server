package memlimit

import (
	"context"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestParseMemMax(t *testing.T) {
	cases := []struct {
		in     string
		want   uint64
		wantOK bool
	}{
		{"max\n", 0, false},
		{"  max  ", 0, false},
		{"", 0, false},
		{"0", 0, false},
		{"536870912\n", 536870912, true},
		{"notanumber", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMemMax(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseMemMax(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestParseMemLimitV1(t *testing.T) {
	cases := []struct {
		in     string
		want   uint64
		wantOK bool
	}{
		{"9223372036854771712\n", 0, false},
		{"0", 0, false},
		{"268435456\n", 268435456, true},
		{"  1073741824 ", 1073741824, true},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMemLimitV1(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseMemLimitV1(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestRatioFromEnv(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"", defaultRatio},
		{"0.75", 0.75},
		{"  0.5 ", 0.5},
		{"1", 1},
		{"0", defaultRatio},
		{"1.5", defaultRatio},
		{"-0.2", defaultRatio},
		{"abc", defaultRatio},
	}
	for _, c := range cases {
		if got := ratioFromEnv(c.in); got != c.want {
			t.Errorf("ratioFromEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestReadMemoryLimitFrom(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	missing := filepath.Join(dir, "does-not-exist")

	t.Run("v2 finite limit wins", func(t *testing.T) {
		got, ok := readMemoryLimitFrom(write("v2-finite", "536870912\n"), missing)
		if !ok || got != 536870912 {
			t.Fatalf("got (%d,%v), want (536870912,true)", got, ok)
		}
	})
	t.Run("v2 max is unlimited and does not fall through to v1", func(t *testing.T) {
		if got, ok := readMemoryLimitFrom(write("v2-max", "max\n"), write("v1-finite", "268435456\n")); ok {
			t.Fatalf("got (%d,%v), want (_,false)", got, ok)
		}
	})
	t.Run("v2 absent uses v1", func(t *testing.T) {
		got, ok := readMemoryLimitFrom(missing, write("v1-only", "268435456\n"))
		if !ok || got != 268435456 {
			t.Fatalf("got (%d,%v), want (268435456,true)", got, ok)
		}
	})
	t.Run("v1 unlimited sentinel", func(t *testing.T) {
		if _, ok := readMemoryLimitFrom(missing, write("v1-unlimited", "9223372036854771712\n")); ok {
			t.Fatal("v1 near-max should read as unlimited")
		}
	})
	t.Run("both absent fails open", func(t *testing.T) {
		if _, ok := readMemoryLimitFrom(missing, missing); ok {
			t.Fatal("both paths missing should fail open")
		}
	})
}

func TestConfigureRespectsExplicitLimit(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
	const explicit = 300 << 20
	debug.SetMemoryLimit(explicit)
	Configure(context.Background(), slog.New(slog.DiscardHandler))
	if got := debug.SetMemoryLimit(-1); got != explicit {
		t.Fatalf("Configure changed an explicit limit: got %d, want %d", got, explicit)
	}
}

func TestConfigureFailsOpenWithoutCgroup(t *testing.T) {
	if _, err := os.Stat(cgroupV2Path); err == nil {
		t.Skip("running inside a cgroup v2 container; cannot assert the no-limit path")
	}
	if _, err := os.Stat(cgroupV1Path); err == nil {
		t.Skip("running inside a cgroup v1 container; cannot assert the no-limit path")
	}
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
	debug.SetMemoryLimit(math.MaxInt64)
	Configure(context.Background(), slog.New(slog.DiscardHandler))
	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Fatalf("Configure set a limit with no cgroup present: %d", got)
	}
}
