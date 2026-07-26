package goroku

import (
	"io"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"goroku/goroku/logger"
)

// L returns the package-level zap logger.
func L() *zap.Logger { return logger.L() }

// InitZapLogging initializes the zap logger.
func InitZapLogging() {
	logger.Init()
}

// SetZapLogOutput mirrors structured application logs to a runtime sink.
func SetZapLogOutput(output io.Writer) {
	logger.SetExtraOutput(output)
}

// SetZapLogLevel applies Tester.tglog_level to the Telegram-only zap sink.
func SetZapLogLevel(level string) {
	minimum := zapcore.ErrorLevel
	switch strings.ToUpper(level) {
	case "DEBUG", "ALL":
		minimum = zapcore.DebugLevel
	case "INFO":
		minimum = zapcore.InfoLevel
	case "WARNING", "WARN":
		minimum = zapcore.WarnLevel
	case "CRITICAL":
		minimum = zapcore.DPanicLevel
	}
	logger.SetExtraMinimumLevel(minimum)
}
