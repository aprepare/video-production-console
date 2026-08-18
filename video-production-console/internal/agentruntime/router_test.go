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

func TestLLMRuntimePreferredUsesGrokWhenEnvEmpty(t *testing.T) {
	t.Setenv(EnvLLMRuntime, "")
	t.Setenv(EnvOpenAIBaseURL, "")
	t.Setenv(EnvOpenAIAPIKey, "")
	t.Setenv("GROK_SEARCH_BASE_URL", "")
	t.Setenv("GROK_SEARCH_API_KEY", "")
	t.Setenv("REMIX_BASE_URL", "")
	t.Setenv("REMIX_API_KEY", "")
	t.Setenv("REMIX_MODEL", "")
	if got := LLMRuntimePreferred(nil); got != RuntimeCodex {
		t.Fatalf("empty secrets = %q", got)
	}
	if got := LLMRuntimePreferred(map[string]string{
		"GROK_SEARCH_BASE_URL": "http://127.0.0.1:2001",
		"GROK_SEARCH_API_KEY":  "test-key",
		"GROK_SEARCH_MODEL":    "cursor-grok-4.6-xhigh-fast",
	}); got != RuntimeOpenAI {
		t.Fatalf("grok secrets = %q", got)
	}
	t.Setenv(EnvLLMRuntime, "codex")
	if got := LLMRuntimePreferred(map[string]string{
		"GROK_SEARCH_BASE_URL": "http://127.0.0.1:2001",
		"GROK_SEARCH_API_KEY":  "test-key",
	}); got != RuntimeCodex {
		t.Fatalf("explicit codex = %q", got)
	}
}

func TestResolveOpenAICompatConfigPrefersRemixSettings(t *testing.T) {
	t.Setenv(EnvOpenAIBaseURL, "")
	t.Setenv(EnvOpenAIAPIKey, "")
	t.Setenv("GROK_SEARCH_BASE_URL", "")
	t.Setenv("GROK_SEARCH_API_KEY", "")
	t.Setenv("GROK_SEARCH_MODEL", "")
	t.Setenv("REMIX_BASE_URL", "")
	t.Setenv("REMIX_API_KEY", "")
	t.Setenv("REMIX_MODEL", "")
	baseURL, apiKey, model, ok := ResolveOpenAICompatConfig(map[string]string{
		"GROK_SEARCH_BASE_URL": "http://grok.invalid/v1",
		"GROK_SEARCH_API_KEY":  "grok-key",
		"GROK_SEARCH_MODEL":    "grok-old",
		"REMIX_BASE_URL":       "http://23.138.12.112:2001/v1",
		"REMIX_API_KEY":        "remix-key",
		"REMIX_MODEL":          "gpt-5.6-sol",
	})
	if !ok || baseURL != "http://23.138.12.112:2001/v1" || apiKey != "remix-key" || model != "gpt-5.6-sol" {
		t.Fatalf("remix config = %q %q %q ok=%t", baseURL, apiKey, model, ok)
	}
}

func TestResolveOpenAICompatConfigPrefersRemixSettingsOverProcessOpenAIEnv(t *testing.T) {
	t.Setenv(EnvOpenAIBaseURL, "https://big-response-arch-percentage.trycloudflare.com/v1")
	t.Setenv(EnvOpenAIAPIKey, "stale-openai-key")
	t.Setenv("GROK_SEARCH_BASE_URL", "")
	t.Setenv("GROK_SEARCH_API_KEY", "")
	t.Setenv("GROK_SEARCH_MODEL", "")
	t.Setenv("REMIX_BASE_URL", "")
	t.Setenv("REMIX_API_KEY", "")
	t.Setenv("REMIX_MODEL", "")
	baseURL, apiKey, model, ok := ResolveOpenAICompatConfig(map[string]string{
		"REMIX_BASE_URL": "http://23.138.12.112:2001/v1",
		"REMIX_API_KEY":  "remix-key",
		"REMIX_MODEL":    "gpt-5.6-sol",
	})
	if !ok || baseURL != "http://23.138.12.112:2001/v1" || apiKey != "remix-key" || model != "gpt-5.6-sol" {
		t.Fatalf("stale openai env won: %q %q %q ok=%t", baseURL, apiKey, model, ok)
	}
}

func TestCompatModelNameKeepsExplicitModel(t *testing.T) {
	if got := CompatModelName("cursor-grok-4.6-xhigh-fast", "grok-fallback"); got != "cursor-grok-4.6-xhigh-fast" {
		t.Fatalf("custom cursor model rewritten: %q", got)
	}
	if got := CompatModelName("gpt-5.6-sol", "cursor-grok-4.6-xhigh-fast"); got != "gpt-5.6-sol" {
		t.Fatalf("explicit gpt model rewritten: %q", got)
	}
	if got := CompatModelName("", "cursor-grok-4.6-xhigh-fast"); got != "cursor-grok-4.6-xhigh-fast" {
		t.Fatalf("empty task model = %q", got)
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
