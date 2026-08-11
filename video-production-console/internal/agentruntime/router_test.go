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

func TestLLMRuntimeFromEnvDefaultsToCodex(t *testing.T) {
	t.Setenv(EnvLLMRuntime, "")
	if got := LLMRuntimeFromEnv(); got != RuntimeCodex {
		t.Fatalf("got %q", got)
	}
	t.Setenv(EnvLLMRuntime, "openai_compat")
	if got := LLMRuntimeFromEnv(); got != RuntimeOpenAI {
		t.Fatalf("got %q", got)
	}
	t.Setenv(EnvLLMRuntime, "pi")
	if got := LLMRuntimeFromEnv(); got != RuntimePi {
		t.Fatalf("got %q", got)
	}
}

func TestSelectRemixOpenAIWhenPreferred(t *testing.T) {
	if got := Select(domain.ActionRemixStandard, RuntimeOpenAI); got != RuntimeOpenAI {
		t.Fatalf("got %q", got)
	}
	if got := Select(domain.ActionTopicBrainstorm, RuntimeCodex); got != RuntimeCodex {
		t.Fatalf("got %q", got)
	}
	if got := Select(domain.ActionMontageExecute, RuntimeOpenAI); got != RuntimeScript {
		t.Fatalf("montage must ignore openai preferred, got %q", got)
	}
}

func TestSelectRemixPiWhenPreferred(t *testing.T) {
	if got := Select(domain.ActionRemixStandard, RuntimePi); got != RuntimePi {
		t.Fatalf("got %q", got)
	}
	if got := Select(domain.ActionTopicDeepen, RuntimePi); got != RuntimePi {
		t.Fatalf("got %q", got)
	}
	if got := Select(domain.ActionMontageExecute, RuntimePi); got != RuntimeScript {
		t.Fatalf("montage must ignore pi preferred, got %q", got)
	}
}

func TestRuntimeSupports(t *testing.T) {
	var script ScriptAdapter
	var codex CodexAdapter
	var openai OpenAIAdapter
	var pi PiAdapter
	if !script.Supports(domain.ActionMontageExecute) {
		t.Fatal("script should support montage.execute")
	}
	if script.Supports(domain.ActionRemixStandard) {
		t.Fatal("script must not claim remix")
	}
	if !codex.Supports(domain.ActionRemixStandard) {
		t.Fatal("codex should support remix")
	}
	if !openai.Supports(domain.ActionRemixStandard) || !openai.Supports(domain.ActionTopicBrainstorm) {
		t.Fatal("openai should support remix/topic")
	}
	if openai.Supports(domain.ActionMontageExecute) {
		t.Fatal("openai must not claim montage")
	}
	if !pi.Supports(domain.ActionRemixStandard) || !pi.Supports(domain.ActionTopicBrainstorm) {
		t.Fatal("pi should support remix/topic")
	}
	if pi.Supports(domain.ActionMontageExecute) {
		t.Fatal("pi must not claim montage")
	}
}
