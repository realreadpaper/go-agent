package llm

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type Provider string

const (
	ProviderOpenAI          Provider = "openai"
	ProviderDeepSeek        Provider = "deepseek"
	ProviderAnthropicCompat Provider = "anthropic_compat"
)

type APIStyle string

const (
	APIStyleOpenAIResponses APIStyle = "openai_responses"
	APIStyleOpenAIChat      APIStyle = "openai_chat"
	APIStyleAnthropic       APIStyle = "anthropic_messages"
)

type Config struct {
	Provider        Provider
	APIStyle        APIStyle
	Model           string
	APIKey          string
	BaseURL         string
	MaxTokens       int
	ReasoningEffort string
	ThinkingEnabled bool
	Store           bool
}

func DefaultConfigFromEnv() (Config, error) {
	provider := Provider(envOr("AGENT_LLM_PROVIDER", string(ProviderOpenAI)))
	cfg := Config{
		Provider:        provider,
		MaxTokens:       intEnv("AGENT_MAX_TOKENS", 8000),
		ReasoningEffort: envOr("AGENT_REASONING_EFFORT", "medium"),
		ThinkingEnabled: boolEnv("AGENT_THINKING_ENABLED", false),
		Store:           boolEnv("AGENT_STORE", false),
	}

	switch provider {
	case ProviderOpenAI:
		cfg.APIStyle = APIStyle(envOr("AGENT_LLM_API_STYLE", string(APIStyleOpenAIResponses)))
		cfg.Model = envOr("AGENT_MODEL", "gpt-5.5")
		cfg.APIKey = os.Getenv("OPENAI_API_KEY")
		cfg.BaseURL = envOr("OPENAI_BASE_URL", "https://api.openai.com")
		if cfg.APIKey == "" {
			return Config{}, errors.New("OPENAI_API_KEY is required when AGENT_LLM_PROVIDER=openai")
		}
	case ProviderDeepSeek:
		cfg.APIStyle = APIStyle(envOr("AGENT_LLM_API_STYLE", string(APIStyleAnthropic)))
		cfg.Model = envOr("AGENT_MODEL", "deepseek-v4-flash")
		cfg.APIKey = os.Getenv("DEEPSEEK_API_KEY")
		if cfg.APIKey == "" {
			return Config{}, errors.New("DEEPSEEK_API_KEY is required when AGENT_LLM_PROVIDER=deepseek")
		}
		switch cfg.APIStyle {
		case APIStyleAnthropic:
			cfg.BaseURL = envOr("DEEPSEEK_BASE_URL", "https://api.deepseek.com/anthropic")
		case APIStyleOpenAIChat:
			cfg.BaseURL = envOr("DEEPSEEK_BASE_URL", "https://api.deepseek.com")
		default:
			return Config{}, fmt.Errorf("unsupported DeepSeek API style %q", cfg.APIStyle)
		}
	case ProviderAnthropicCompat:
		cfg.APIStyle = APIStyleAnthropic
		cfg.Model = envOr("AGENT_MODEL", "")
		cfg.APIKey = envOr("ANTHROPIC_API_KEY", os.Getenv("DEEPSEEK_API_KEY"))
		cfg.BaseURL = envOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
		if cfg.APIKey == "" {
			return Config{}, errors.New("ANTHROPIC_API_KEY is required when AGENT_LLM_PROVIDER=anthropic_compat")
		}
	default:
		return Config{}, fmt.Errorf("unsupported provider %q", provider)
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func boolEnv(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	case "0", "false", "FALSE", "no", "NO", "off", "OFF":
		return false
	default:
		return fallback
	}
}
