package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pikachu0310/whisper-discord-bot/internal/config"
	"github.com/pikachu0310/whisper-discord-bot/internal/discordbot"
	"github.com/pikachu0310/whisper-discord-bot/internal/whisper"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("設定の読み込みに失敗: %v", err)
	}

	whisperClient := whisper.New(cfg.FWSBaseURL)
	bot, err := discordbot.New(cfg.DiscordToken, cfg.TranscriptChannelID, whisperClient)
	if err != nil {
		log.Fatalf("Bot の初期化に失敗: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := bot.Run(ctx); err != nil {
		log.Fatalf("Bot が異常終了: %v", err)
	}
}
