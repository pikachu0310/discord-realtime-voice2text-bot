package voice

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
	messageWindow       = 2 * time.Minute
	silenceThreshold    = 1 * time.Second
	minSegmentDuration  = 250 * time.Millisecond
	minAverageAmplitude = 600
)

// Manager is responsible for joining/leaving voice channels and handling transcription.
type Manager struct {
	session             *discordgo.Session
	whisperClient       *whisper.Client
	aggregator          *transcript.Aggregator
	transcriptChannelID string

	voiceMu              sync.Mutex
	activeVoiceListeners map[string]*voiceHandler
}

type voiceHandler struct {
	conn      *discordgo.VoiceConnection
	cancel    context.CancelFunc
	segmenter *audio.Segmenter
	resolver  *ssrcResolver
}

// NewManager creates a Manager that posts transcripts to transcriptChannelID.
func NewManager(session *discordgo.Session, whisperClient *whisper.Client, transcriptChannelID string) *Manager {
	return &Manager{
		session:              session,
		whisperClient:        whisperClient,
		transcriptChannelID:  transcriptChannelID,
		activeVoiceListeners: make(map[string]*voiceHandler),
		aggregator: transcript.NewAggregator(
			transcriptChannelID,
			transcript.DiscordPoster{Session: session},
			messageWindow,
		),
	}
}

// Join starts listening to the user's current voice channel within the guild.
func (m *Manager) Join(guildID, userID string) error {
	channelID, err := m.findUserVoiceChannel(guildID, userID)
	if err != nil {
		return err
	}
	return m.joinVoiceChannel(guildID, channelID)
}

// Leave stops listening within the guild.
func (m *Manager) Leave(guildID string) error {
	return m.leaveVoiceChannel(guildID)
}

// Shutdown stops all active listeners.
func (m *Manager) Shutdown() {
	m.voiceMu.Lock()
	defer m.voiceMu.Unlock()

	for guildID, handler := range m.activeVoiceListeners {
		handler.cancel()
		if handler.segmenter != nil {
			handler.segmenter.Stop()
		}
		if handler.conn != nil {
			handler.conn.Disconnect()
			handler.conn.Close()
		}
		delete(m.activeVoiceListeners, guildID)
	}
}

func (m *Manager) joinVoiceChannel(guildID, channelID string) error {
	log.Printf("joining voice channel guild=%s channel=%s", guildID, channelID)
	m.voiceMu.Lock()
	if handler, ok := m.activeVoiceListeners[guildID]; ok {
		if handler.conn != nil && handler.conn.ChannelID == channelID {
			m.voiceMu.Unlock()
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
		delete(m.activeVoiceListeners, guildID)
	}
	m.voiceMu.Unlock()

	vc, err := m.session.ChannelVoiceJoin(guildID, channelID, false, false)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	segmenter := audio.NewSegmenter(guildID, silenceThreshold, m.consumeSegment)
	resolver := newSSRCResolver()
	vc.LogLevel = discordgo.LogInformational
	vc.AddSSRCMappingHandler(func(vc *discordgo.VoiceConnection, ssrc uint32, userID string) {
		resolver.set(ssrc, userID)
		log.Printf("voice ssrc mapping guild=%s user=%s ssrc=%d", vc.GuildID, userID, ssrc)
	})
	vc.AddHandler(func(vc *discordgo.VoiceConnection, vs *discordgo.VoiceSpeakingUpdate) {
		if vs == nil {
			return
		}
		log.Printf("voice speaking update guild=%s user=%s speaking=%t ssrc=%d", vc.GuildID, vs.UserID, vs.Speaking, vs.SSRC)
	})

	receiver := audio.NewReceiver(segmenter, resolver)
	receiver.Start(ctx, vc)
	log.Printf("voice receiver started guild=%s channel=%s", guildID, channelID)

	m.voiceMu.Lock()
	m.activeVoiceListeners[guildID] = &voiceHandler{
		conn:      vc,
		cancel:    cancel,
		segmenter: segmenter,
		resolver:  resolver,
	}
	m.voiceMu.Unlock()

	return nil
}

func (m *Manager) leaveVoiceChannel(guildID string) error {
	m.voiceMu.Lock()
	handler, ok := m.activeVoiceListeners[guildID]
	if ok {
		delete(m.activeVoiceListeners, guildID)
	}
	m.voiceMu.Unlock()
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

func (m *Manager) findUserVoiceChannel(guildID, userID string) (string, error) {
	guild, err := m.session.State.Guild(guildID)
	if err != nil {
		guild, err = m.session.Guild(guildID)
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

func (m *Manager) consumeSegment(guildID, userID string, samples []int16) {
	if len(samples) == 0 {
		return
	}
	if ok, reason := shouldSendSegment(samples); !ok {
		log.Printf("segment skipped guild=%s user=%s (%s)", guildID, userID, reason)
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
	text, err := m.whisperClient.Transcribe(ctx, tmp.Name())
	if err != nil {
		log.Printf("transcription failed: %v", err)
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		log.Printf("empty transcription guild=%s user=%s", guildID, userID)
		return
	}
	displayName := m.displayName(guildID, userID)
	line := fmt.Sprintf("%s: 「%s」", displayName, text)
	if err := m.aggregator.AddLine(line); err != nil {
		log.Printf("aggregator add line failed: %v", err)
		return
	}
	log.Printf("posted transcription guild=%s line=%s", guildID, line)
}

func (m *Manager) displayName(guildID, userID string) string {
	member, err := m.session.State.Member(guildID, userID)
	if err != nil || member == nil {
		member, err = m.session.GuildMember(guildID, userID)
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

func shouldSendSegment(samples []int16) (bool, string) {
	if len(samples) == 0 {
		return false, "no samples"
	}
	actualDuration := time.Duration(len(samples)) * time.Second / (time.Duration(audio.SampleRate) * time.Duration(audio.Channels))
	if actualDuration < minSegmentDuration {
		return false, fmt.Sprintf("duration %.2fs < %.2fs", actualDuration.Seconds(), minSegmentDuration.Seconds())
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
	if avg < int64(minAverageAmplitude) {
		return false, fmt.Sprintf("avg amplitude %d < %d", avg, minAverageAmplitude)
	}
	return true, ""
}
