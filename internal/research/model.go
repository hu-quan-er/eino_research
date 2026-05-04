package research

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

type ModelConfig struct {
	APIKey  string
	Model   string
	BaseURL string
	Timeout time.Duration
}

func NewOpenAICompatibleModel(ctx context.Context, cfg ModelConfig) (model.ToolCallingChatModel, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
		BaseURL: cfg.BaseURL,
		Timeout: cfg.Timeout,
	})
	if err != nil {
		return nil, err
	}

	return chatModel, nil
}
