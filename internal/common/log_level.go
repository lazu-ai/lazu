package common

import (
	"os"
	"strings"
)

// 日志级别: debug < info < warn < error
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

var LogLevel = LogLevelInfo

func init() {
	ConfigureLogLevelFromEnv()
}

func ConfigureLogLevelFromEnv() {
	level := normalizeLogLevel(os.Getenv("LOG_LEVEL"))
	if level == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("DEBUG")), "true") {
			level = LogLevelDebug
		} else {
			level = LogLevelInfo
		}
	}
	LogLevel = level
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case LogLevelDebug:
		return LogLevelDebug
	case LogLevelInfo:
		return LogLevelInfo
	case "warning", LogLevelWarn:
		return LogLevelWarn
	case LogLevelError:
		return LogLevelError
	case "silent", "off", "none":
		return "silent"
	default:
		return ""
	}
}

func ShouldLog(level string) bool {
	levelOrder := map[string]int{
		LogLevelDebug: 0,
		LogLevelInfo:  1,
		LogLevelWarn:  2,
		LogLevelError: 3,
		"silent":      4,
	}

	current, ok := levelOrder[LogLevel]
	if !ok {
		current = 1
	}

	target, ok := levelOrder[level]
	if !ok {
		target = 1
	}

	return target >= current
}
