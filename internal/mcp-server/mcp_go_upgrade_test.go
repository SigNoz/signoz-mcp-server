package mcp_server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSDKLoggerBoundsPersistentAttrs(t *testing.T) {
	var logs bytes.Buffer
	logger := newBufferedLogger(&logs, slog.LevelDebug)
	sdkLogger := slog.New(&sdkLogHandler{next: logger.Handler()}).With(
		slog.String("session_id", strings.Repeat("x", 8*1024)),
	)
	sdkLogger.Error("transport rejection")

	record, _ := logRecordByMessage(t, &logs, "transport rejection")
	value, ok := record["session_id"].(string)
	if !ok || len(value) > 4*1024 || !strings.HasSuffix(value, "...(truncated)") {
		t.Fatalf("persistent SDK attr = %T len=%d, want bounded string", record["session_id"], len(value))
	}
}
