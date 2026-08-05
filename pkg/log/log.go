package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

const (
	truncBodyLimit      = 4 * 1024
	requestCaptureLimit = 1024 * 1024
	truncBodySuffix     = "...(truncated)"
	redactedValue       = "[REDACTED]"
)

var (
	sensitiveKeyNormalizer = strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	sensitiveKeySuffixes   = [...]string{
		"apikey",
		"credential",
		"credentials",
		"password",
		"passwd",
		"passphrase",
		"privatekey",
		"routingkey",
		"secret",
		"token",
		"webhookurl",
	}
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

	// Use stderr so stdio transport can keep stdout reserved for MCP frames.
	baseHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
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

	return slog.New(NewContextHandler(baseHandler)).With(
		slog.String("service.version", version.Version),
	)
}

func ErrAttr(err error) slog.Attr {
	return slog.Any("error", err)
}

type boundedLogError string

func (e boundedLogError) Error() string { return string(e) }

// BoundedErrAttr preserves error semantics for ContextHandler while capping
// client- or upstream-controlled text before it reaches the log pipeline.
func BoundedErrAttr(err error) slog.Attr {
	if err == nil {
		return slog.Any("error", nil)
	}
	return slog.Any("error", boundedLogError(TruncBody([]byte(err.Error()))))
}

// LevelForError maps an error to the severity it should be logged at.
// context.Canceled means the caller (typically an MCP client) disconnected or
// aborted the request mid-flight — expected behavior logged at DEBUG so it
// does not pollute ERROR streams. context.DeadlineExceeded and every other
// error stay ERROR: timeouts are real operational signals. Callers must still
// emit the record at the returned level — never drop it (fail open, never
// fail silent).
func LevelForError(err error) slog.Level {
	if errors.Is(err, context.Canceled) {
		return slog.LevelDebug
	}
	return slog.LevelError
}

func TruncBody(b []byte) string {
	return truncBodyAt(b, truncBodyLimit)
}

func truncBodyAt(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}

	cutoff := limit - len(truncBodySuffix)
	if cutoff < 0 {
		cutoff = 0
	}

	return string(b[:cutoff]) + truncBodySuffix
}

// TruncAny marshals v to JSON and applies TruncBody so structured values
// (e.g. response bodies of unknown size) can be logged without leaking
// unbounded payloads into stdout or the collector pipeline.
func TruncAny(v any) string {
	return truncAnyAt(v, truncBodyLimit)
}

func truncAnyAt(v any, limit int) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unmarshalable>"
	}
	return truncBodyAt(b, limit)
}

// RedactedTruncAny serializes a structured log payload while replacing values
// under credential-shaped keys at any depth. It is intended for diagnostic
// request capture: callers retain the payload's reproducible shape and
// non-secret values without copying credentials into logs. Request captures
// use a dedicated 1 MiB cap; ordinary body and error logs remain capped at
// 4 KiB.
func RedactedTruncAny(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<unmarshalable>"
	}

	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "<unmarshalable>"
	}
	decoded = redactSensitiveValues(decoded)
	return truncAnyAt(decoded, requestCaptureLimit)
}

func redactSensitiveValues(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalizedKey := normalizeLogKey(key)
			if isSensitiveNormalizedKey(normalizedKey) {
				typed[key] = redactedValue
				continue
			}
			typed[key] = redactSensitiveValues(child)
		}
	case []any:
		for i, child := range typed {
			typed[i] = redactSensitiveValues(child)
		}
	}
	return value
}

func normalizeLogKey(key string) string {
	return sensitiveKeyNormalizer.Replace(strings.ToLower(strings.TrimSpace(key)))
}

func isSensitiveNormalizedKey(normalized string) bool {
	if normalized == "authorization" || normalized == "slackapiurl" {
		return true
	}
	for _, suffix := range sensitiveKeySuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
