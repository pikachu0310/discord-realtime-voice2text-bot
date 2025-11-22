package search

import (
	"context"
	"strings"
)

// Result represents a single web search hit.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Provider defines a search backend implementation.
type Provider interface {
	Search(query string) ([]Result, error)
	SearchWithContext(ctx context.Context, query string) ([]Result, error)
	Name() string
}

// FormatResults renders results for LLM tool consumption.
func FormatResults(results []Result) string {
	if len(results) == 0 {
		return "検索結果はありませんでした。"
	}
	var b strings.Builder
	for i, r := range results {
		if i >= 5 {
			break
		}
		b.WriteString("- ")
		if r.Title != "" {
			b.WriteString(r.Title)
		} else {
			b.WriteString("(no title)")
		}
		if r.URL != "" {
			b.WriteString(" | ")
			b.WriteString(r.URL)
		}
		if r.Snippet != "" {
			b.WriteString("\n  ")
			b.WriteString(strings.TrimSpace(r.Snippet))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
