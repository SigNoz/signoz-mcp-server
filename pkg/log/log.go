package log

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
)

const (
	truncBodyLimit  = 4 * 1024
	truncBodySuffix = "...(truncated)"
)

// New creates a JSON slog logger that matches the Zeus field naming convention.
func New(level string) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	baseHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slogLevel,
		AddSource: true,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.SourceKey:
				a.Key = "code"
			case slog.TimeKey:
				a.Key = "timestamp"
			}
			return a
		},
	})

	return slog.New(NewContextHandler(baseHandler))
}

func ErrAttr(err error) slog.Attr {
	return slog.Any("error", err)
}

func TruncBody(b []byte) string {
	if len(b) <= truncBodyLimit {
		return string(b)
	}

	cutoff := truncBodyLimit - len(truncBodySuffix)
	if cutoff < 0 {
		cutoff = 0
	}

	return string(b[:cutoff]) + truncBodySuffix
}

// TruncAny marshals v to JSON and applies TruncBody so structured values
// (e.g. response bodies of unknown size) can be logged without leaking
// unbounded payloads into stdout or the collector pipeline.
func TruncAny(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unmarshalable>"
	}
	return TruncBody(b)
}
