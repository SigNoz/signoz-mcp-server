package segmentanalytics

import (
	"fmt"

	segment "github.com/segmentio/analytics-go/v3"
	"go.uber.org/zap"
)

type zapSegmentLogger struct {
	logger *zap.Logger
}

func newSegmentLogger(logger *zap.Logger) segment.Logger {
	return &zapSegmentLogger{logger: logger.Named("segment")}
}

func (l *zapSegmentLogger) Logf(format string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func (l *zapSegmentLogger) Errorf(format string, args ...interface{}) {
	l.logger.Error(fmt.Sprintf(format, args...))
}
