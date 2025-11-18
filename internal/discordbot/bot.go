package discordbot

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/pikachu0310/whisper-discord-bot/internal/audio"
	"github.com/pikachu0310/whisper-discord-bot/internal/transcript"
	"github.com/pikachu0310/whisper-discord-bot/internal/whisper"
)

const (
	messageWindow    = 2 * time.Minute
	silenceThreshold = 1 * time.Second
)

// Bot is the core Discord bot application.
type Bot struct {
	session              *discordgo.Session
	whisperClient        *whisper.Client
	aggregator           *transcript.Aggregator
	transcriptChannelID  string
	voiceMu              sync.Mutex
	activeVoiceListeners map[string]*voiceHandler
}

type voiceHandler struct {
	conn      *discordgo.VoiceConnection
	cancel    context.CancelFunc
	segmenter *audio.Segmenter
	resolver  *ssrcResolver
}

// New creates a ready-to-run bot.
func New(token, transcriptChannelID string, whisperClient *whisper.Client) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	session.StateEnabled = true
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildVoiceStates |
		discordgo.IntentsMessageContent

	bot := &Bot{
		session:              session,
		whisperClient:        whisperClient,
		transcriptChannelID:  transcriptChannelID,
		activeVoiceListeners: make(map[string]*voiceHandler),
	}
	bot.aggregator = transcript.NewAggregator(transcriptChannelID, transcript.DiscordPoster{Session: session}, messageWindow)

	session.AddHandler(bot.handleMessageCreate)

	return bot, nil
}

// Run opens the Discord gateway and blocks until ctx is canceled.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord session: %w", err)
	}
	log.Println("bot is running")
	defer b.session.Close()

	<-ctx.Done()
	b.shutdown()
	return nil
}

func (b *Bot) shutdown() {
	b.voiceMu.Lock()
	defer b.voiceMu.Unlock()

	for guildID, handler := range b.activeVoiceListeners {
		handler.cancel()
		if handler.segmenter != nil {
			handler.segmenter.Stop()
		}
		if handler.conn != nil {
			handler.conn.Disconnect()
			handler.conn.Close()
		}
		delete(b.activeVoiceListeners, guildID)
	}
}

func (b *Bot) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot || m.GuildID == "" {
		return
	}
	switch strings.TrimSpace(m.Content) {
	case "!join":
		chID, err := b.findUserVoiceChannel(m.GuildID, m.Author.ID)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("VC を特定できません: %v", err))
			return
		}
		if err := b.joinVoiceChannel(m.GuildID, chID); err != nil {
			log.Printf("failed to join voice: %v", err)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("参加に失敗しました: %v", err))
			return
		}
		s.ChannelMessageSend(m.ChannelID, "参加しました。")
	case "!leave":
		if err := b.leaveVoiceChannel(m.GuildID); err != nil {
			log.Printf("failed to leave voice: %v", err)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("退出に失敗しました: %v", err))
			return
		}
		s.ChannelMessageSend(m.ChannelID, "退出しました。")
	}
}

func (b *Bot) joinVoiceChannel(guildID, channelID string) error {
	b.voiceMu.Lock()
	if handler, ok := b.activeVoiceListeners[guildID]; ok {
		if handler.conn != nil && handler.conn.ChannelID == channelID {
			b.voiceMu.Unlock()
			return nil
		}
		handler.cancel()
		if handler.segmenter != nil {
			handler.segmenter.Stop()
		}
		if handler.conn != nil {
			handler.conn.Disconnect()
			handler.conn.Close()
		}
		delete(b.activeVoiceListeners, guildID)
	}
	b.voiceMu.Unlock()

	vc, err := b.session.ChannelVoiceJoin(guildID, channelID, false, true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	segmenter := audio.NewSegmenter(guildID, silenceThreshold, b.consumeSegment)
	resolver := newSSRCResolver()
	vc.AddHandler(func(vc *discordgo.VoiceConnection, vs *discordgo.VoiceSpeakingUpdate) {
		if vs == nil {
			return
		}
		resolver.set(uint32(vs.SSRC), vs.UserID)
	})
	receiver := audio.NewReceiver(segmenter, resolver.resolve)
	receiver.Start(ctx, vc)

	b.voiceMu.Lock()
	b.activeVoiceListeners[guildID] = &voiceHandler{
		conn:      vc,
		cancel:    cancel,
		segmenter: segmenter,
		resolver:  resolver,
	}
	b.voiceMu.Unlock()

	return nil
}

func (b *Bot) leaveVoiceChannel(guildID string) error {
	b.voiceMu.Lock()
	handler, ok := b.activeVoiceListeners[guildID]
	if ok {
		delete(b.activeVoiceListeners, guildID)
	}
	b.voiceMu.Unlock()
	if !ok {
		return fmt.Errorf("ボイスチャンネルに接続していません")
	}

	handler.cancel()
	if handler.segmenter != nil {
		handler.segmenter.Stop()
	}
	if handler.conn != nil {
		handler.conn.Disconnect()
		handler.conn.Close()
	}
	return nil
}

func (b *Bot) findUserVoiceChannel(guildID, userID string) (string, error) {
	guild, err := b.session.State.Guild(guildID)
	if err != nil {
		guild, err = b.session.Guild(guildID)
		if err != nil {
			return "", err
		}
	}
	for _, vs := range guild.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID, nil
		}
	}
	return "", fmt.Errorf("ユーザーは VC に接続していません")
}

func (b *Bot) consumeSegment(guildID, userID string, samples []int16) {
	if len(samples) == 0 {
		return
	}
	tmp, err := os.CreateTemp("", "segment-*.wav")
	if err != nil {
		log.Printf("create temp file failed: %v", err)
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	if err := audio.WritePCM16ToWAV(tmp.Name(), samples, audio.SampleRate, audio.Channels); err != nil {
		log.Printf("write wav failed: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	text, err := b.whisperClient.Transcribe(ctx, tmp.Name())
	if err != nil {
		log.Printf("transcription failed: %v", err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	displayName := b.displayName(guildID, userID)
	line := fmt.Sprintf("%s: 「%s」", displayName, text)
	if err := b.aggregator.AddLine(line); err != nil {
		log.Printf("aggregator add line failed: %v", err)
	}
}

func (b *Bot) displayName(guildID, userID string) string {
	member, err := b.session.State.Member(guildID, userID)
	if err != nil || member == nil {
		member, err = b.session.GuildMember(guildID, userID)
		if err != nil || member == nil {
			return userID
		}
	}
	if member.Nick != "" {
		return member.Nick
	}
	if member.User != nil && member.User.Username != "" {
		return member.User.Username
	}
	return userID
}

type ssrcResolver struct {
	mu      sync.RWMutex
	mapping map[uint32]string
}

func newSSRCResolver() *ssrcResolver {
	return &ssrcResolver{
		mapping: make(map[uint32]string),
	}
}

func (r *ssrcResolver) set(ssrc uint32, userID string) {
	if userID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mapping[ssrc] = userID
}

func (r *ssrcResolver) resolve(ssrc uint32) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	userID, ok := r.mapping[ssrc]
	return userID, ok
}
