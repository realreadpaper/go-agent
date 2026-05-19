package llm

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveOpenAIResponses(t *testing.T) {
	loadNearestDotEnv(t)
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY is required")
	}

	cfg := Config{
		Provider:        ProviderOpenAI,
		APIStyle:        APIStyleOpenAIResponses,
		Model:           envOr("AGENT_MODEL", "gpt-5.5"),
		APIKey:          key,
		BaseURL:         envOr("OPENAI_BASE_URL", "https://api.openai.com"),
		MaxTokens:       64,
		ReasoningEffort: envOr("AGENT_REASONING_EFFORT", "medium"),
	}
	t.Logf("OpenAI live config: provider=%s style=%s model=%s base_url=%s api_key=%s reasoning=%s",
		cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, redactSecret(cfg.APIKey), cfg.ReasoningEffort)
	client, err := NewClient(cfg, http.DefaultClient)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	resp, err := client.Create(Request{
		System:   "Reply with a short plain text answer.",
		Messages: []Message{{Role: "user", Content: "Say pong."}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	text := textFrom(resp)
	t.Logf("OpenAI live response: stop_reason=%s text=%q", resp.StopReason, text)
	if text == "" {
		t.Fatalf("response contained no text: %#v", resp)
	}
}

func TestLiveDeepSeek(t *testing.T) {
	loadNearestDotEnv(t)
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY is required")
	}

	style := APIStyle(envOr("DEEPSEEK_API_STYLE", string(APIStyleAnthropic)))
	baseURL := os.Getenv("DEEPSEEK_BASE_URL")
	if baseURL == "" && style == APIStyleAnthropic {
		baseURL = "https://api.deepseek.com/anthropic"
	}
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	cfg := Config{
		Provider:  ProviderDeepSeek,
		APIStyle:  style,
		Model:     envOr("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		APIKey:    key,
		BaseURL:   baseURL,
		MaxTokens: 64,
	}
	t.Logf("DeepSeek live config: provider=%s style=%s model=%s base_url=%s api_key=%s",
		cfg.Provider, cfg.APIStyle, cfg.Model, cfg.BaseURL, redactSecret(cfg.APIKey))
	client, err := NewClient(cfg, http.DefaultClient)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	resp, err := client.Create(Request{
		System:   "Reply with a short plain text answer.",
		Messages: []Message{{Role: "user", Content: "Say pong."}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	text := textFrom(resp)
	t.Logf("DeepSeek live response: stop_reason=%s text=%q", resp.StopReason, text)
	if text == "" {
		t.Fatalf("response contained no text: %#v", resp)
	}
}

func textFrom(resp Response) string {
	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func redactSecret(value string) string {
	if len(value) <= 12 {
		return "***"
	}
	return value[:7] + "..." + value[len(value)-4:]
}

func loadNearestDotEnv(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			loadDotEnvFile(t, path)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func loadDotEnvFile(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open .env: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if os.Getenv(key) == "" {
			t.Setenv(key, value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan .env: %v", err)
	}
}
