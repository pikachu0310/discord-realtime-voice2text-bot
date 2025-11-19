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
	minSegmentDuration   = 400 * time.Millisecond
	minAverageAmplitude  = 900
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
	log.Printf("[guild=%s channel=%s] command from %s: %s", m.GuildID, m.ChannelID, m.Author.ID, m.Content)
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
	log.Printf("joining voice channel guild=%s channel=%s", guildID, channelID)
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

	vc, err := b.session.ChannelVoiceJoin(guildID, channelID, false, false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	segmenter := audio.NewSegmenter(guildID, silenceThreshold, b.consumeSegment)
	resolver := newSSRCResolver()
	vc.LogLevel = discordgo.LogInformational
	vc.AddHandler(func(vc *discordgo.VoiceConnection, vs *discordgo.VoiceSpeakingUpdate) {
		if vs == nil {
			return
		}
		resolver.set(uint32(vs.SSRC), vs.UserID)
		log.Printf("voice speaking update guild=%s user=%s speaking=%t ssrc=%d", vc.GuildID, vs.UserID, vs.Speaking, vs.SSRC)
	})
	receiver := audio.NewReceiver(segmenter, resolver)
	receiver.Start(ctx, vc)
	log.Printf("voice receiver started guild=%s channel=%s", guildID, channelID)

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
	if !shouldSendSegment(samples) {
		log.Printf("segment skipped due to low volume or short duration guild=%s user=%s", guildID, userID)
		return
	}
	log.Printf("segment ready guild=%s user=%s samples=%d", guildID, userID, len(samples))
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

	log.Printf("transcribing guild=%s user=%s file=%s", guildID, userID, tmp.Name())
	text, err := b.whisperClient.Transcribe(ctx, tmp.Name())
	if err != nil {
		log.Printf("transcription failed: %v", err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		log.Printf("empty transcription guild=%s user=%s", guildID, userID)
		return
	}
	displayName := b.displayName(guildID, userID)
	line := fmt.Sprintf("%s: 「%s」", displayName, text)
	if err := b.aggregator.AddLine(line); err != nil {
		log.Printf("aggregator add line failed: %v", err)
		return
	}
	log.Printf("posted transcription guild=%s line=%s", guildID, line)
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
	mu      sync.Mutex
	mapping map[uint32]string
	waiters map[uint32][]chan string
}

func newSSRCResolver() *ssrcResolver {
	return &ssrcResolver{
		mapping: make(map[uint32]string),
		waiters: make(map[uint32][]chan string),
	}
}

func (r *ssrcResolver) set(ssrc uint32, userID string) {
	if userID == "" {
		return
	}
	r.mu.Lock()
	r.mapping[ssrc] = userID
	waiters := r.waiters[ssrc]
	delete(r.waiters, ssrc)
	r.mu.Unlock()

	for _, ch := range waiters {
		ch <- userID
		close(ch)
	}
}

func (r *ssrcResolver) Resolve(ssrc uint32) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	userID, ok := r.mapping[ssrc]
	return userID, ok
}

func (r *ssrcResolver) Wait(ssrc uint32, timeout time.Duration) (string, bool) {
	r.mu.Lock()
	if userID, ok := r.mapping[ssrc]; ok {
		r.mu.Unlock()
		return userID, true
	}
	ch := make(chan string, 1)
	r.waiters[ssrc] = append(r.waiters[ssrc], ch)
	r.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case userID := <-ch:
		return userID, true
	case <-timer.C:
		r.mu.Lock()
		if waiters, ok := r.waiters[ssrc]; ok {
			for i, w := range waiters {
				if w == ch {
					waiters = append(waiters[:i], waiters[i+1:]...)
					break
				}
			}
			if len(waiters) == 0 {
				delete(r.waiters, ssrc)
			} else {
				r.waiters[ssrc] = waiters
			}
		}
		r.mu.Unlock()
		return "", false
	}
}

func shouldSendSegment(samples []int16) bool {
	if len(samples) == 0 {
		return false
	}
	duration := time.Duration(len(samples)) * time.Second / (time.Duration(audio.SampleRate) * time.Duration(audio.Channels))
	if duration < minSegmentDuration {
		return false
	}
	var sum int64
	for _, sample := range samples {
		if sample < 0 {
			sum -= int64(sample)
		} else {
			sum += int64(sample)
		}
	}
	avg := sum / int64(len(samples))
	return avg >= minAverageAmplitude
}
