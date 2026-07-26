package logger

import (
	"io"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Logger *zap.Logger
var loggerMu sync.RWMutex
var outputMu sync.RWMutex
var extraOutput io.Writer
var extraMinimumLevel = zapcore.ErrorLevel

// SetExtraOutput mirrors zap records to an optional runtime sink.
func SetExtraOutput(output io.Writer) {
	outputMu.Lock()
	extraOutput = output
	outputMu.Unlock()
	Init()
}

// SetExtraMinimumLevel controls the minimum level mirrored to the runtime sink.
func SetExtraMinimumLevel(level zapcore.Level) {
	outputMu.Lock()
	extraMinimumLevel = level
	outputMu.Unlock()
	Init()
}

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

	// Colour belongs on the console only. Sharing encoderConfig put ANSI escapes
	// into the JSON "level" field on disk ("[34mINFO[0m"), which
	// breaks log parsers and level filtering.
	fileEncoderConfig := encoderConfig
	fileEncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	fileEncoder := zapcore.NewJSONEncoder(fileEncoderConfig)

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
	outputMu.RLock()
	extra := extraOutput
	extraLevel := extraMinimumLevel
	outputMu.RUnlock()
	if extra != nil {
		telegramEncoderConfig := encoderConfig
		telegramEncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
		telegramCore := zapcore.NewCore(
			zapcore.NewJSONEncoder(telegramEncoderConfig),
			zapcore.AddSync(extra),
			zap.LevelEnablerFunc(func(entryLevel zapcore.Level) bool {
				return entryLevel >= level && entryLevel >= extraLevel
			}),
		)
		core = zapcore.NewTee(core, telegramCore)
	}
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
