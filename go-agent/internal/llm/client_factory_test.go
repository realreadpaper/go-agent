package llm

import (
	"net/http"
	"testing"
)

func TestNewClientRoutesSupportedProviderStyles(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "openai responses",
			cfg:  Config{Provider: ProviderOpenAI, APIStyle: APIStyleOpenAIResponses, Model: "gpt-test", APIKey: "key", BaseURL: "https://example.test"},
		},
		{
			name: "openai chat",
			cfg:  Config{Provider: ProviderOpenAI, APIStyle: APIStyleOpenAIChat, Model: "gpt-test", APIKey: "key", BaseURL: "https://example.test"},
		},
		{
			name: "deepseek anthropic",
			cfg:  Config{Provider: ProviderDeepSeek, APIStyle: APIStyleAnthropic, Model: "deepseek-v4-pro", APIKey: "key", BaseURL: "https://example.test"},
		},
		{
			name: "deepseek chat",
			cfg:  Config{Provider: ProviderDeepSeek, APIStyle: APIStyleOpenAIChat, Model: "deepseek-v4-flash", APIKey: "key", BaseURL: "https://example.test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.cfg, http.DefaultClient)
			if err != nil {
				t.Fatalf("NewClient returned error: %v", err)
			}
			if client == nil {
				t.Fatal("NewClient returned nil client")
			}
		})
	}
}

func TestNewClientRejectsUnsupportedProviderStyle(t *testing.T) {
	_, err := NewClient(Config{
		Provider: ProviderOpenAI,
		APIStyle: APIStyleAnthropic,
		Model:    "gpt-test",
		APIKey:   "key",
		BaseURL:  "https://example.test",
	}, http.DefaultClient)
	if err == nil {
		t.Fatal("NewClient returned nil error for unsupported provider/style")
	}
}
