package render

import (
	"encoding/json"

	"github.com/hu-quan-er/eino_research/internal/research"
)

// JSON 将 ResearchResult 渲染为缩进 JSON。
//
// 该格式保留所有结构化字段，适合调试、自动化评估或被其他系统消费。
func JSON(result research.ResearchResult) (string, error) {
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
