package segmentanalytics

import (
	"fmt"
	"log/slog"

	segment "github.com/segmentio/analytics-go/v3"
)

type slogSegmentLogger struct {
	logger *slog.Logger
}

func newSegmentLogger(logger *slog.Logger) segment.Logger {
	return &slogSegmentLogger{logger: logger.With(slog.String("component", "segment"))}
}

func (l *slogSegmentLogger) Logf(format string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func (l *slogSegmentLogger) Errorf(format string, args ...interface{}) {
	l.logger.Error(fmt.Sprintf(format, args...))
}
