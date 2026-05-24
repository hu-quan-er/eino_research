package research

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// ModelConfig 是 research 层创建 OpenAI-compatible 模型所需的最小配置。
type ModelConfig struct {
	// APIKey 是 OpenAI-compatible 服务访问密钥。
	APIKey string
	// Model 是模型名称。
	Model string
	// BaseURL 是兼容 OpenAI API 的服务地址；为空时使用 SDK 默认地址。
	BaseURL string
	// Timeout 是单次模型请求超时时间。
	Timeout time.Duration
}

// NewOpenAICompatibleModel 创建支持 tool calling 的 OpenAI-compatible chat model。
//
// BaseURL 可用于接入兼容 OpenAI API 的第三方网关或本地模型服务。
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
