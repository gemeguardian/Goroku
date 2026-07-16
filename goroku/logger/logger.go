package logger

import (
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Logger *zap.Logger
var loggerMu sync.RWMutex

func Init() {
	logger := build()
	loggerMu.Lock()
	Logger = logger
	loggerMu.Unlock()
}

func build() *zap.Logger {
	level := zapcore.InfoLevel
	if os.Getenv("GOROKU_DEBUG") == "1" {
		level = zapcore.DebugLevel
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	fileEncoder := zapcore.NewJSONEncoder(encoderConfig)

	consoleCore := zapcore.NewCore(
		consoleEncoder,
		zapcore.AddSync(os.Stdout),
		level,
	)

	fileWriter := &lumberjack.Logger{
		Filename:   "goroku.log",
		MaxSize:    10,
		MaxBackups: 1,
		LocalTime:  true,
	}
	fileCore := zapcore.NewCore(
		fileEncoder,
		zapcore.AddSync(fileWriter),
		level,
	)

	core := zapcore.NewTee(consoleCore, fileCore)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}

func L() *zap.Logger {
	loggerMu.RLock()
	logger := Logger
	loggerMu.RUnlock()
	if logger != nil {
		return logger
	}

	logger = build()
	loggerMu.Lock()
	if Logger == nil {
		Logger = logger
	} else {
		logger = Logger
	}
	loggerMu.Unlock()
	return logger
}
