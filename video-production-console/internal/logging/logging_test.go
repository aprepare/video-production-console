package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"testing"
)

func TestParseLevelDefaultsToInfo(t *testing.T) {
	for value, expected := range map[string]slog.Level{
		"debug":   slog.LevelDebug,
		" DEBUG ": slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo,
		"verbose": slog.LevelInfo,
	} {
		if got := parseLevel(value); got != expected {
			t.Fatalf("parseLevel(%q) = %v, want %v", value, got, expected)
		}
	}
}

func TestInitEmitsJSONAndHonoursLevel(t *testing.T) {
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })

	logger := Init(func(string) (string, bool) { return "warn", true })
	if logger == nil {
		t.Fatal("Init returned no logger")
	}
	if logger.Enabled(nil, slog.LevelInfo) {
		t.Fatal("info must be filtered out when the level is warn")
	}
	if !logger.Enabled(nil, slog.LevelWarn) {
		t.Fatal("warn must be enabled when the level is warn")
	}

	var buffer bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, nil)))
	slog.Default().Error("structured event", "project_id", "p-1")
	// slog.SetDefault also redirects the standard library logger.
	log.Print("legacy line")

	decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
	var structured struct {
		Level     string `json:"level"`
		Message   string `json:"msg"`
		ProjectID string `json:"project_id"`
	}
	if err := decoder.Decode(&structured); err != nil {
		t.Fatalf("decode structured record: %v", err)
	}
	if structured.Level != "ERROR" || structured.Message != "structured event" || structured.ProjectID != "p-1" {
		t.Fatalf("unexpected structured record: %+v", structured)
	}
	var bridged struct {
		Message string `json:"msg"`
	}
	if err := decoder.Decode(&bridged); err != nil {
		t.Fatalf("decode bridged record: %v", err)
	}
	if bridged.Message != "legacy line" {
		t.Fatalf("standard library log was not bridged: %+v", bridged)
	}
}

func TestStdLoggerAndPrintfStayStructured(t *testing.T) {
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })
	var buffer bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelWarn})))

	StdLogger(slog.LevelWarn).Printf("reconciliation warning %d", 7)
	Printf(slog.Default(), slog.LevelWarn)("queued %d job(s)", 2)
	// A level below the handler threshold must stay filtered out.
	Printf(slog.Default(), slog.LevelInfo)("ignored")

	var records []struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
	}
	decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
	for decoder.More() {
		var record struct {
			Level   string `json:"level"`
			Message string `json:"msg"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %s", len(records), buffer.String())
	}
	if records[0].Level != "WARN" || records[0].Message != "reconciliation warning 7" {
		t.Fatalf("unexpected standard library record: %+v", records[0])
	}
	if records[1].Level != "WARN" || records[1].Message != "queued 2 job(s)" {
		t.Fatalf("unexpected Printf record: %+v", records[1])
	}
}

func TestInitWithoutEnvironmentKeepsInfo(t *testing.T) {
	restore := slog.Default()
	t.Cleanup(func() { slog.SetDefault(restore) })

	logger := Init(func(string) (string, bool) { return "", false })
	if !logger.Enabled(nil, slog.LevelInfo) {
		t.Fatal("info must be enabled by default")
	}
	if logger.Enabled(nil, slog.LevelDebug) {
		t.Fatal("debug must be filtered out by default")
	}
}
