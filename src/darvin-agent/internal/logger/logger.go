// Package logger wires the zap + lumberjack logger for the agent process.
package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger struct {
	*zap.Logger
}

var globalLogger *Logger

func Init(cfg *Config) error {
	level := parseLevel(cfg.Level)

	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var encoder zapcore.Encoder
	if cfg.Encoding == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// The Go agent writes its Gateway port as the single line on stdout
	// (see internal/gateway.Server.Start), so any deployment that parses
	// that line must set Output to "stderr".
	var sink zapcore.WriteSyncer
	switch cfg.Output {
	case "stderr":
		sink = zapcore.AddSync(os.Stderr)
	default:
		sink = zapcore.AddSync(os.Stdout)
	}

	core := zapcore.NewCore(encoder, sink, zapLevel)

	if cfg.Output == "file" && cfg.Filename != "" {
		fileWriter := zapcore.AddSync(&lumberjack.Logger{
			Filename:   cfg.Filename,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
		})
		core = zapcore.NewTee(
			core,
			zapcore.NewCore(encoder, fileWriter, zapLevel),
		)
	}

	zapLogger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	globalLogger = &Logger{Logger: zapLogger}

	return nil
}

func Get() *Logger {
	if globalLogger == nil {
		panic("logger not initialized, call Init first")
	}
	return globalLogger
}

func parseLevel(level string) string {
	if level == "" {
		return "info"
	}
	return level
}
