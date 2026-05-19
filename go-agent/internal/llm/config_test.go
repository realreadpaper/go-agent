package llm

import "testing"

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AGENT_LLM_PROVIDER",
		"AGENT_LLM_API_STYLE",
		"AGENT_MODEL",
		"AGENT_MAX_TOKENS",
		"AGENT_REASONING_EFFORT",
		"AGENT_THINKING_ENABLED",
		"AGENT_STORE",
		"AGENT_TRACE_RAW_API",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"DEEPSEEK_API_KEY",
		"DEEPSEEK_BASE_URL",
	} {
		t.Setenv(key, "")
	}
}

func TestDefaultConfigFromEnvParsesOpenAIResponses(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AGENT_LLM_PROVIDER", "openai")
	t.Setenv("AGENT_LLM_API_STYLE", "openai_responses")
	t.Setenv("AGENT_MODEL", "gpt-5.5")
	t.Setenv("OPENAI_API_KEY", "test-key")

	cfg, err := DefaultConfigFromEnv()
	if err != nil {
		t.Fatalf("DefaultConfigFromEnv returned error: %v", err)
	}

	if cfg.Provider != ProviderOpenAI {
		t.Fatalf("Provider = %q, want openai", cfg.Provider)
	}
	if cfg.APIStyle != APIStyleOpenAIResponses {
		t.Fatalf("APIStyle = %q, want openai_responses", cfg.APIStyle)
	}
	if cfg.Model != "gpt-5.5" {
		t.Fatalf("Model = %q, want gpt-5.5", cfg.Model)
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("APIKey = %q, want test-key", cfg.APIKey)
	}
	if cfg.BaseURL != "https://api.openai.com" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.ReasoningEffort != "medium" {
		t.Fatalf("ReasoningEffort = %q, want medium", cfg.ReasoningEffort)
	}
	if cfg.Store {
		t.Fatal("Store = true, want false by default")
	}
	if cfg.TraceRawAPI {
		t.Fatal("TraceRawAPI = true, want false by default")
	}
}

func TestDefaultConfigFromEnvParsesTraceRawAPI(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AGENT_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("AGENT_TRACE_RAW_API", "1")

	cfg, err := DefaultConfigFromEnv()
	if err != nil {
		t.Fatalf("DefaultConfigFromEnv returned error: %v", err)
	}

	if !cfg.TraceRawAPI {
		t.Fatal("TraceRawAPI = false, want true")
	}
}

func TestDefaultConfigFromEnvParsesDeepSeekAnthropicDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AGENT_LLM_PROVIDER", "deepseek")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")

	cfg, err := DefaultConfigFromEnv()
	if err != nil {
		t.Fatalf("DefaultConfigFromEnv returned error: %v", err)
	}

	if cfg.Provider != ProviderDeepSeek {
		t.Fatalf("Provider = %q, want deepseek", cfg.Provider)
	}
	if cfg.APIStyle != APIStyleAnthropic {
		t.Fatalf("APIStyle = %q, want anthropic_messages", cfg.APIStyle)
	}
	if cfg.Model != "deepseek-v4-flash" {
		t.Fatalf("Model = %q, want deepseek-v4-flash", cfg.Model)
	}
	if cfg.BaseURL != "https://api.deepseek.com/anthropic" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestDefaultConfigFromEnvAllowsDeepSeekOpenAIChat(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AGENT_LLM_PROVIDER", "deepseek")
	t.Setenv("AGENT_LLM_API_STYLE", "openai_chat")
	t.Setenv("AGENT_MODEL", "deepseek-v4-flash")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")

	cfg, err := DefaultConfigFromEnv()
	if err != nil {
		t.Fatalf("DefaultConfigFromEnv returned error: %v", err)
	}

	if cfg.APIStyle != APIStyleOpenAIChat {
		t.Fatalf("APIStyle = %q, want openai_chat", cfg.APIStyle)
	}
	if cfg.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestDefaultConfigFromEnvRequiresProviderAPIKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("AGENT_LLM_PROVIDER", "openai")

	_, err := DefaultConfigFromEnv()
	if err == nil {
		t.Fatal("DefaultConfigFromEnv returned nil error without OPENAI_API_KEY")
	}
}
