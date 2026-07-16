package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"goroku/goroku"

	"go.uber.org/zap"
)

// executionAuditEvent records a dangerous capability invocation without secret bodies.
type executionAuditEvent struct {
	ActorID    int64
	ChatID     int64
	Capability string
	Digest     string
	Duration   time.Duration
	ExitCode   int
	TimedOut   bool
	Canceled   bool
	Truncated  bool
	Status     string
}

var (
	auditFileMu   sync.Mutex
	auditFilePath string
)

func contentSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func contentSHA256String(s string) string {
	return contentSHA256([]byte(s))
}

func auditStatus(err error, timedOut, canceled bool) string {
	switch {
	case timedOut:
		return "timeout"
	case canceled:
		return "canceled"
	case err != nil:
		return "error"
	default:
		return "ok"
	}
}

func auditExecution(ev executionAuditEvent) {
	if ev.Status == "" {
		ev.Status = "ok"
	}
	fields := []zap.Field{
		zap.String("event", "execution_audit"),
		zap.Int64("actor_id", ev.ActorID),
		zap.Int64("chat_id", ev.ChatID),
		zap.String("capability", ev.Capability),
		zap.String("content_digest", ev.Digest),
		zap.Duration("duration", ev.Duration),
		zap.Int("exit_code", ev.ExitCode),
		zap.Bool("timed_out", ev.TimedOut),
		zap.Bool("canceled", ev.Canceled),
		zap.Bool("truncated", ev.Truncated),
		zap.String("status", ev.Status),
	}
	if logger := goroku.L(); logger != nil {
		logger.Info("execution_audit", fields...)
	}
	appendExecutionAuditFile(ev)
}

func appendExecutionAuditFile(ev executionAuditEvent) {
	path := executionAuditLogPath()
	if path == "" {
		return
	}
	line := fmt.Sprintf(
		"ts=%s actor=%d chat=%d capability=%s digest=%s duration_ms=%d exit=%d timed_out=%t canceled=%t truncated=%t status=%s\n",
		time.Now().UTC().Format(time.RFC3339Nano),
		ev.ActorID,
		ev.ChatID,
		ev.Capability,
		ev.Digest,
		ev.Duration.Milliseconds(),
		ev.ExitCode,
		ev.TimedOut,
		ev.Canceled,
		ev.Truncated,
		ev.Status,
	)
	auditFileMu.Lock()
	defer auditFileMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
}

func executionAuditLogPath() string {
	if auditFilePath != "" {
		return auditFilePath
	}
	if goroku.BaseDir == "" {
		return ""
	}
	return filepath.Join(goroku.BaseDir, "execution_audit.log")
}

func setExecutionAuditLogPathForTest(path string) {
	auditFileMu.Lock()
	auditFilePath = path
	auditFileMu.Unlock()
}
