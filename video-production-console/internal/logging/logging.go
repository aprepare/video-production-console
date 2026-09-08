// Package logging installs the console's process-wide structured logger.
package logging

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LevelEnvironmentKey selects the minimum level of the process-wide logger.
const LevelEnvironmentKey = "VIDEO_CONSOLE_LOG_LEVEL"

// FileEnvironmentKey overrides where the log file goes. Unset: video-console-data/logs/console-YYYYMMDD.log
// under the working directory when that data directory exists; "off" disables the file sink.
// 2026-09-08：exe 是隐藏窗口起的，stderr 谁也看不见，拆分镜慢在哪一块查不出来，所以默认落一份文件。
const FileEnvironmentKey = "VIDEO_CONSOLE_LOG_FILE"

// Init installs a JSON handler as the process-wide slog logger. slog.SetDefault
// also routes the standard library logger through the same handler, so
// components that still require a *log.Logger stay on one output format. An
// unset or unrecognized level keeps info. Output goes to stderr and, when
// available, to a daily log file (see FileEnvironmentKey).
func Init(lookupEnv func(string) (string, bool)) *slog.Logger {
	level := slog.LevelInfo
	if lookupEnv != nil {
		if value, ok := lookupEnv(LevelEnvironmentKey); ok {
			level = parseLevel(value)
		}
	}
	var out io.Writer = os.Stderr
	if file := openLogFile(lookupEnv); file != nil {
		out = io.MultiWriter(os.Stderr, file)
	}
	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}

func openLogFile(lookupEnv func(string) (string, bool)) *os.File {
	path := ""
	if lookupEnv != nil {
		if value, ok := lookupEnv(FileEnvironmentKey); ok {
			path = strings.TrimSpace(value)
		}
	}
	if strings.EqualFold(path, "off") {
		return nil
	}
	if path == "" {
		if info, err := os.Stat("video-console-data"); err != nil || !info.IsDir() {
			return nil // 不是从仓库根目录起的（比如测试），不落文件
		}
		path = filepath.Join("video-console-data", "logs", "console-"+time.Now().Format("20060102")+".log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return file
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
