package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	inner *slog.Logger
}

func New(level string) *Logger {
	return &Logger{
		inner: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: parseLevel(level),
		})),
	}
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (l *Logger) With(args ...any) *Logger {
	if l == nil {
		return New("info")
	}
	return &Logger{inner: l.inner.With(args...)}
}

func (l *Logger) Debug(msg string, args ...any) {
	if l == nil {
		return
	}
	l.inner.DebugContext(context.Background(), msg, args...)
}

func (l *Logger) Info(msg string, args ...any) {
	if l == nil {
		return
	}
	l.inner.InfoContext(context.Background(), msg, args...)
}

func (l *Logger) Error(msg string, args ...any) {
	if l == nil {
		return
	}
	l.inner.ErrorContext(context.Background(), msg, args...)
}
