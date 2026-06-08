package memlimit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMemMax(t *testing.T) {
	cases := []struct {
		in       string
		want     uint64
		wantOK   bool
		describe string
	}{
		{"max\n", 0, false, "v2 unlimited"},
		{"  max  ", 0, false, "v2 unlimited padded"},
		{"", 0, false, "empty"},
		{"0", 0, false, "zero"},
		{"536870912\n", 536870912, true, "512 MiB"},
		{"notanumber", 0, false, "garbage"},
	}
	for _, c := range cases {
		got, ok := parseMemMax(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: parseMemMax(%q) = (%d,%v), want (%d,%v)", c.describe, c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestParseMemLimitV1(t *testing.T) {
	cases := []struct {
		in     string
		want   uint64
		wantOK bool
	}{
		{"9223372036854771712\n", 0, false}, // near-max unlimited sentinel
		{"0", 0, false},
		{"268435456\n", 268435456, true}, // 256 MiB
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
		{"0", defaultRatio},    // out of range → default
		{"1.5", defaultRatio},  // > 1 → default
		{"-0.2", defaultRatio}, // negative → default
		{"abc", defaultRatio},  // unparseable → default
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
		v2 := write("v2-finite", "536870912\n")
		got, ok := readMemoryLimitFrom(v2, missing)
		if !ok || got != 536870912 {
			t.Fatalf("got (%d,%v), want (536870912,true)", got, ok)
		}
	})

	t.Run("v2 max → unlimited, does not fall through to v1", func(t *testing.T) {
		v2 := write("v2-max", "max\n")
		v1 := write("v1-finite", "268435456\n")
		if got, ok := readMemoryLimitFrom(v2, v1); ok {
			t.Fatalf("got (%d,%v), want (_,false) — v2 present means v1 is ignored", got, ok)
		}
	})

	t.Run("v2 absent → v1 used", func(t *testing.T) {
		v1 := write("v1-only", "268435456\n")
		got, ok := readMemoryLimitFrom(missing, v1)
		if !ok || got != 268435456 {
			t.Fatalf("got (%d,%v), want (268435456,true)", got, ok)
		}
	})

	t.Run("v1 unlimited sentinel → false", func(t *testing.T) {
		v1 := write("v1-unlimited", "9223372036854771712\n")
		if _, ok := readMemoryLimitFrom(missing, v1); ok {
			t.Fatalf("v1 near-max should read as unlimited (ok=false)")
		}
	})

	t.Run("both absent → false", func(t *testing.T) {
		if _, ok := readMemoryLimitFrom(missing, missing); ok {
			t.Fatalf("both paths missing should fail open (ok=false)")
		}
	})
}
