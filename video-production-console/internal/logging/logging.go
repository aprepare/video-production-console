// Package logging installs the console's process-wide structured logger.
package logging

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
)

// LevelEnvironmentKey selects the minimum level of the process-wide logger.
const LevelEnvironmentKey = "VIDEO_CONSOLE_LOG_LEVEL"

// Init installs a JSON handler as the process-wide slog logger. slog.SetDefault
// also routes the standard library logger through the same handler, so
// components that still require a *log.Logger stay on one output format. An
// unset or unrecognized level keeps info.
func Init(lookupEnv func(string) (string, bool)) *slog.Logger {
	level := slog.LevelInfo
	if lookupEnv != nil {
		if value, ok := lookupEnv(LevelEnvironmentKey); ok {
			level = parseLevel(value)
		}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}

// StdLogger adapts the process-wide logger for dependencies that still require
// a *log.Logger, so their output stays in the structured stream.
func StdLogger(level slog.Level) *log.Logger {
	return slog.NewLogLogger(slog.Default().Handler(), level)
}

// Printf adapts the process-wide logger for callers that take a Printf-style
// logging function. Prefer passing attributes to a *slog.Logger directly.
func Printf(logger *slog.Logger, level slog.Level) func(string, ...any) {
	return func(format string, args ...any) {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Log(context.Background(), level, fmt.Sprintf(format, args...))
	}
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
