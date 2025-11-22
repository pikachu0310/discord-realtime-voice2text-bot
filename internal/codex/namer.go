package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ThreadNamer creates human-friendly Discord thread names.
type ThreadNamer struct {
	GeminiAPIKey string
	httpClient   *http.Client
}

// NewThreadNamer returns a configured namer.
func NewThreadNamer(apiKey string) *ThreadNamer {
	return &ThreadNamer{
		GeminiAPIKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Generate returns a thread name. If Gemini is available it is used, otherwise a fallback is returned.
func (n *ThreadNamer) Generate(ctx context.Context, prompt, fallbackID string) string {
	prompt = strings.TrimSpace(prompt)
	if n.GeminiAPIKey == "" || prompt == "" {
		return fallbackName(fallbackID)
	}

	name, err := n.generateWithGemini(ctx, prompt)
	if err != nil || name == "" {
		return fallbackName(fallbackID)
	}
	return name
}

func (n *ThreadNamer) generateWithGemini(ctx context.Context, prompt string) (string, error) {
	reqBody := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{
						"text": fmt.Sprintf("次の内容を要約して最大40文字のスレッド名を作ってください。記号や引用符は避け、簡潔な日本語にしてください。内容: %s", prompt),
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens": 32,
			"temperature":     0.4,
		},
	}
	data, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", n.GeminiAPIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini status %d", resp.StatusCode)
	}

	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	for _, cand := range decoded.Candidates {
		for _, part := range cand.Content.Parts {
			name := strings.TrimSpace(part.Text)
			if name != "" {
				return truncate(name, 90), nil
			}
		}
	}
	return "", fmt.Errorf("gemini returned empty name")
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func fallbackName(fallbackID string) string {
	now := time.Now().Format("20060102-1504")
	if fallbackID == "" {
		return fmt.Sprintf("thread-%s", now)
	}
	suffix := fallbackID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return fmt.Sprintf("thread-%s-%s", now, suffix)
}
