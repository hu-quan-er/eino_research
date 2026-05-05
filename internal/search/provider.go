package search

import (
	"context"
	"strconv"
	"strings"
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

func DeduplicateStable(in []Source) []Source {
	seenURL := make(map[string]struct{}, len(in))
	usedID := make(map[string]struct{}, len(in))
	out := make([]Source, 0, len(in))
	nextID := 1

	for _, source := range in {
		if source.URL == "" {
			continue
		}
		if _, ok := seenURL[source.URL]; ok {
			continue
		}

		source.ID = stableSourceID(source.ID, usedID, &nextID)
		seenURL[source.URL] = struct{}{}
		usedID[source.ID] = struct{}{}
		out = append(out, source)
	}

	return out
}

func stableSourceID(id string, used map[string]struct{}, next *int) string {
	id = strings.TrimSpace(id)
	if id != "" {
		if _, ok := used[id]; !ok {
			return id
		}
	}
	for {
		candidate := "src_" + strconv.Itoa(*next)
		*next = *next + 1
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
}
