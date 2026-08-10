package main

import (
	"context"
	"testing"
)

func TestRunRejectsMissingAndUnsafeMaintenanceArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"backup"},
		{"check"},
		{"restore"},
	} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("run(%v) unexpectedly succeeded", args)
		}
	}
}

func TestRunCheckRejectsNonDatabase(t *testing.T) {
	if err := run(context.Background(), []string{"check", "-database", t.TempDir()}); err == nil {
		t.Fatal("check accepted a directory as a database")
	}
}
