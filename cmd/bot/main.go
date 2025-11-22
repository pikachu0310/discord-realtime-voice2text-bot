package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/pikachu0310/whisper-discord-bot/internal/codex"
	"github.com/pikachu0310/whisper-discord-bot/internal/config"
	"github.com/pikachu0310/whisper-discord-bot/internal/discordbot"
	"github.com/pikachu0310/whisper-discord-bot/internal/whisper"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("設定の読み込みに失敗: %v", err)
	}

	whisperClient := whisper.New(cfg.FWSBaseURL)
	store, err := codex.NewStore(cfg.StatePath)
	if err != nil {
		log.Fatalf("Codex セッションストアの初期化に失敗: %v", err)
	}

	namer := codex.NewThreadNamer(cfg.GeminiAPIKey)
	codexClient := codex.Client{Model: cfg.CodexModel}

	bot, err := discordbot.New(cfg.DiscordToken, cfg.TranscriptChannelID, whisperClient, store, namer, codexClient)
	if err != nil {
		log.Fatalf("Bot の初期化に失敗: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := bot.Run(ctx); err != nil {
		log.Fatalf("Bot が異常終了: %v", err)
	}
}

func loadDotEnv() {
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(".env"); err != nil {
			log.Printf(".env の読み込みに失敗しました: %v", err)
		}
		return
	} else if !os.IsNotExist(err) {
		log.Printf(".env の存在確認に失敗しました: %v", err)
	}
}
