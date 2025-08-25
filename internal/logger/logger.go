package logger

import (
	"go.uber.org/zap"
)

// New can help to set development or production
func New() (*zap.Logger, error) {
	return zap.NewProduction()
}
