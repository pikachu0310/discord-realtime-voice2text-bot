package config

import (
	"fmt"
	"os"
)

const DefaultFWSBaseURL = "http://localhost:8000"

// Config represents runtime configuration from environment variables.
type Config struct {
	DiscordToken        string
	TranscriptChannelID string
	FWSBaseURL          string
}

// Load reads configuration from environment variables and validates it.
func Load() (Config, error) {
	cfg := Config{
		DiscordToken:        os.Getenv("DISCORD_TOKEN"),
		TranscriptChannelID: os.Getenv("TRANSCRIPT_CHANNEL_ID"),
		FWSBaseURL:          os.Getenv("FWS_BASE_URL"),
	}

	if cfg.FWSBaseURL == "" {
		cfg.FWSBaseURL = DefaultFWSBaseURL
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
