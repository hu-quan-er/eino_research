package render

import (
	"encoding/json"

	"github.com/hu-quan-er/eino_research/internal/research"
)

func JSON(result research.ResearchResult) (string, error) {
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
