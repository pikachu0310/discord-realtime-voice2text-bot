package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// BingProvider implements Provider using Bing Web Search API.
type BingProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewBing returns a Bing provider.
func NewBing(apiKey string) *BingProvider {
	return &BingProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (p *BingProvider) Name() string {
	return "bing"
}

// Search executes a query with a background context.
func (p *BingProvider) Search(query string) ([]Result, error) {
	return p.SearchWithContext(context.Background(), query)
}

// SearchWithContext executes a query.
func (p *BingProvider) SearchWithContext(ctx context.Context, query string) ([]Result, error) {
	endpoint := fmt.Sprintf("https://api.bing.microsoft.com/v7.0/search?q=%s", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build bing request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bing request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bing returned status %d", resp.StatusCode)
	}

	var decoded struct {
		WebPages struct {
			Value []struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"value"`
		} `json:"webPages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode bing response: %w", err)
	}

	results := make([]Result, 0, len(decoded.WebPages.Value))
	for _, r := range decoded.WebPages.Value {
		results = append(results, Result{
			Title:   r.Name,
			URL:     r.URL,
			Snippet: r.Snippet,
		})
	}
	return results, nil
}
