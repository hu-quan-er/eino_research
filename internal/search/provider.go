package search

import (
	"context"
	"strconv"
)

type Source struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
	Provider string `json:"provider"`
	Query    string `json:"query"`
}

type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]Source, error)
}

func Deduplicate(in []Source) []Source {
	seen := make(map[string]struct{}, len(in))
	out := make([]Source, 0, len(in))

	for _, source := range in {
		if source.URL == "" {
			continue
		}
		if _, ok := seen[source.URL]; ok {
			continue
		}

		seen[source.URL] = struct{}{}
		source.ID = "src_" + strconv.Itoa(len(out)+1)
		out = append(out, source)
	}

	return out
}
