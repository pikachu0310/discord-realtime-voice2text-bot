package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TavilyProvider implements Provider using Tavily Search API.
type TavilyProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewTavily returns a Tavily provider.
func NewTavily(apiKey string) *TavilyProvider {
	return &TavilyProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (p *TavilyProvider) Name() string {
	return "tavily"
}

// Search executes a query with a background context.
func (p *TavilyProvider) Search(query string) ([]Result, error) {
	return p.SearchWithContext(context.Background(), query)
}

// SearchWithContext executes a query.
func (p *TavilyProvider) SearchWithContext(ctx context.Context, query string) ([]Result, error) {
	payload := map[string]interface{}{
		"api_key":     p.apiKey,
		"query":       query,
		"max_results": 5,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode tavily request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build tavily request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tavily returned status %d", resp.StatusCode)
	}

	var decoded struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode tavily response: %w", err)
	}

	results := make([]Result, 0, len(decoded.Results))
	for _, r := range decoded.Results {
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return results, nil
}
