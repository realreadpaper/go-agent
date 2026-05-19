package llm

import (
	"fmt"
	"net/http"
)

type DeepSeekClient struct {
	inner Client
}

func NewDeepSeekClient(cfg Config, httpClient *http.Client) (*DeepSeekClient, error) {
	switch cfg.APIStyle {
	case APIStyleAnthropic:
		return &DeepSeekClient{inner: NewAnthropicCompatClient(cfg, httpClient)}, nil
	case APIStyleOpenAIChat:
		return &DeepSeekClient{inner: NewOpenAIChatClient(cfg, httpClient)}, nil
	default:
		return nil, fmt.Errorf("unsupported DeepSeek API style %q", cfg.APIStyle)
	}
}

func (d *DeepSeekClient) Create(req Request) (Response, error) {
	return d.inner.Create(req)
}
