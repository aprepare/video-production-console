package agentruntime

import (
	"testing"

	"video-production-console/internal/domain"
)

func TestMontageRuntimeFromEnvDefaultsToScript(t *testing.T) {
	t.Setenv(EnvMontageRuntime, "")
	if got := MontageRuntimeFromEnv(); got != RuntimeScript {
		t.Fatalf("got %q, want script", got)
	}
	t.Setenv(EnvMontageRuntime, "codex")
	if got := MontageRuntimeFromEnv(); got != RuntimeCodex {
		t.Fatalf("got %q, want codex", got)
	}
	t.Setenv(EnvMontageRuntime, "SCRIPT")
	if got := MontageRuntimeFromEnv(); got != RuntimeScript {
		t.Fatalf("got %q, want script", got)
	}
}

func TestSelectMontageDefaultsToScript(t *testing.T) {
	if got := Select(domain.ActionMontageExecute, RuntimeScript); got != RuntimeScript {
		t.Fatalf("got %q", got)
	}
	if got := Select(domain.ActionMontageExecute, RuntimeCodex); got != RuntimeCodex {
		t.Fatalf("got %q", got)
	}
	if got := Select(domain.ActionRemixStandard, RuntimeScript); got != RuntimeCodex {
		t.Fatalf("non-montage must stay codex, got %q", got)
	}
}

func TestRuntimeSupports(t *testing.T) {
	var script ScriptAdapter
	var codex CodexAdapter
	if !script.Supports(domain.ActionMontageExecute) {
		t.Fatal("script should support montage.execute")
	}
	if script.Supports(domain.ActionRemixStandard) {
		t.Fatal("script must not claim remix")
	}
	if !codex.Supports(domain.ActionRemixStandard) {
		t.Fatal("codex should support remix")
	}
}
