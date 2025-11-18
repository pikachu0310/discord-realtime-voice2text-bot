package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const defaultTimeout = 2 * time.Minute

// Client talks to faster-whisper-server using an OpenAI-compatible API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new Client with the provided baseURL.
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// Transcribe uploads an audio file and returns the text transcription.
func (c *Client) Transcribe(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open audio file: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err = io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy audio data: %w", err)
	}

	if err := writer.WriteField("language", "ja"); err != nil {
		return "", fmt.Errorf("set language field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finalize multipart body: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1/audio/transcriptions", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcribe request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("transcribe failed: status %d body %s", resp.StatusCode, string(b))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode transcription: %w", err)
	}
	return result.Text, nil
}
