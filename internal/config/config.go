package config

import (
	"fmt"
	"os"
)

const (
	DefaultFWSBaseURL = "http://localhost:8000"
	DefaultStatePath  = "data/codex_sessions.json"
	DefaultCodexModel = "gpt-5-minimal"
)

// Config represents runtime configuration from environment variables.
type Config struct {
	DiscordToken        string
	TranscriptChannelID string
	FWSBaseURL          string
	StatePath           string
	GeminiAPIKey        string
	CodexModel          string
}

// Load reads configuration from environment variables and validates it.
func Load() (Config, error) {
	cfg := Config{
		DiscordToken:        os.Getenv("DISCORD_TOKEN"),
		TranscriptChannelID: os.Getenv("TRANSCRIPT_CHANNEL_ID"),
		FWSBaseURL:          os.Getenv("FWS_BASE_URL"),
		StatePath:           os.Getenv("CODEX_STATE_PATH"),
		GeminiAPIKey:        os.Getenv("GEMINI_API_KEY"),
		CodexModel:          os.Getenv("CODEX_MODEL"),
	}

	if cfg.FWSBaseURL == "" {
		cfg.FWSBaseURL = DefaultFWSBaseURL
	}
	if cfg.StatePath == "" {
		cfg.StatePath = DefaultStatePath
	}
	if cfg.CodexModel == "" {
		cfg.CodexModel = DefaultCodexModel
	}

	var missing []string
	if cfg.DiscordToken == "" {
		missing = append(missing, "DISCORD_TOKEN")
	}
	if cfg.TranscriptChannelID == "" {
		missing = append(missing, "TRANSCRIPT_CHANNEL_ID")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %v", missing)
	}

	return cfg, nil
}
