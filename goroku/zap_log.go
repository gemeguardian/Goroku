package goroku

import (
	"go.uber.org/zap"

	"goroku/goroku/logger"
)

// L returns the package-level zap logger.
func L() *zap.Logger { return logger.L() }

// InitZapLogging initializes the zap logger.
func InitZapLogging() {
	logger.Init()
}
