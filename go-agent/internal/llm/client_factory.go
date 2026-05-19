package llm

import (
	"fmt"
	"net/http"
)

func NewClient(cfg Config, httpClient *http.Client) (Client, error) {
	switch {
	case cfg.Provider == ProviderOpenAI && cfg.APIStyle == APIStyleOpenAIResponses:
		return NewOpenAIResponsesClient(cfg, httpClient), nil
	case cfg.Provider == ProviderOpenAI && cfg.APIStyle == APIStyleOpenAIChat:
		return NewOpenAIChatClient(cfg, httpClient), nil
	case cfg.Provider == ProviderDeepSeek && cfg.APIStyle == APIStyleAnthropic:
		return NewDeepSeekClient(cfg, httpClient)
	case cfg.Provider == ProviderDeepSeek && cfg.APIStyle == APIStyleOpenAIChat:
		return NewDeepSeekClient(cfg, httpClient)
	case cfg.Provider == ProviderAnthropicCompat && cfg.APIStyle == APIStyleAnthropic:
		return NewAnthropicCompatClient(cfg, httpClient), nil
	default:
		return nil, fmt.Errorf(
			"unsupported provider/API style combination %q/%q; allowed: openai/openai_responses, openai/openai_chat, deepseek/anthropic_messages, deepseek/openai_chat, anthropic_compat/anthropic_messages",
			cfg.Provider,
			cfg.APIStyle,
		)
	}
}
