package montage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type montageLogRecord struct {
	Message    string `json:"msg"`
	Level      string `json:"level"`
	TaskID     string `json:"task_id"`
	Code       string `json:"code"`
	Phase      string `json:"phase"`
	ExitCode   int    `json:"exit_code"`
	StderrTail string `json:"stderr_tail"`
}

func captureMontageLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })
	buffer := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, nil)))
	return buffer
}

func decodeMontageLog(t *testing.T, buffer *bytes.Buffer) montageLogRecord {
	t.Helper()
	var record montageLogRecord
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("decode log record %q: %v", buffer.String(), err)
	}
	return record
}

func TestReportFailureLogsTaskID(t *testing.T) {
	buffer := captureMontageLog(t)

	(&Coordinator{}).reportFailure("task-77", "registration_failed", errors.New("draft rejected"))

	record := decodeMontageLog(t, buffer)
	if record.TaskID != "task-77" || record.Code != "registration_failed" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Level != "WARN" || record.Phase != "registration" {
		t.Fatalf("registration warning is not correlated: %+v", record)
	}
}

func TestLogScriptFailureCarriesTaskIDAndStderrTail(t *testing.T) {
	buffer := captureMontageLog(t)

	logScriptFailure(context.Background(), "task-88", "register",
		CommandResult{Stderr: []byte("Traceback\nPermissionError: 剪映草稿目录被占用\n"), ExitCode: 3},
		"registration command failed")

	record := decodeMontageLog(t, buffer)
	if record.TaskID != "task-88" || record.ExitCode != 3 {
		t.Fatalf("unexpected record: %+v", record)
	}
	if !strings.Contains(record.StderrTail, "PermissionError") {
		t.Fatalf("stderr tail was dropped: %+v", record)
	}
}

func TestStderrTailKeepsBoundedValidUTF8(t *testing.T) {
	tail := stderrTail([]byte(strings.Repeat("剪", maxScriptStderrTail)))
	if len(tail) > maxScriptStderrTail {
		t.Fatalf("tail length = %d, want <= %d", len(tail), maxScriptStderrTail)
	}
	if !strings.HasPrefix(tail, "剪") {
		t.Fatalf("tail was cut inside a rune: %q", tail[:12])
	}
}
