package transcript

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Poster abstracts Discord message send/edit operations for testing.
type Poster interface {
	SendMessage(channelID, content string) (string, error)
	EditMessage(channelID, messageID, content string) error
}

// DiscordPoster implements Poster using a discordgo session.
type DiscordPoster struct {
	Session *discordgo.Session
}

// SendMessage posts to a channel and returns the message ID.
func (p DiscordPoster) SendMessage(channelID, content string) (string, error) {
	if p.Session == nil {
		return "", fmt.Errorf("session is nil")
	}
	msg, err := p.Session.ChannelMessageSend(channelID, content)
	if err != nil {
		return "", err
	}
	return msg.ID, nil
}

// EditMessage edits an existing Discord message.
func (p DiscordPoster) EditMessage(channelID, messageID, content string) error {
	if p.Session == nil {
		return fmt.Errorf("session is nil")
	}
	_, err := p.Session.ChannelMessageEdit(channelID, messageID, content)
	return err
}

type messageState struct {
	id    string
	lines []string
	timer *time.Timer
}

// Aggregator batches transcription lines into a single Discord message with a timeout.
type Aggregator struct {
	channelID string
	poster    Poster
	window    time.Duration

	mu      sync.Mutex
	current *messageState
}

// NewAggregator creates an Aggregator.
func NewAggregator(channelID string, poster Poster, window time.Duration) *Aggregator {
	return &Aggregator{
		channelID: channelID,
		poster:    poster,
		window:    window,
	}
}

// AddLine appends a transcription line, editing the last message inside the time window.
func (a *Aggregator) AddLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.current == nil {
		msgID, err := a.poster.SendMessage(a.channelID, line)
		if err != nil {
			return err
		}
		state := &messageState{
			id:    msgID,
			lines: []string{line},
		}
		a.current = state
		a.resetTimerLocked(state)
		return nil
	}

	lines := append(append([]string{}, a.current.lines...), line)
	content := strings.Join(lines, "\n")
	if err := a.poster.EditMessage(a.channelID, a.current.id, content); err != nil {
		return err
	}
	a.current.lines = lines
	a.resetTimerLocked(a.current)
	return nil
}

func (a *Aggregator) resetTimerLocked(state *messageState) {
	if state.timer != nil {
		state.timer.Stop()
	}
	state.timer = time.AfterFunc(a.window, func() {
		a.mu.Lock()
		defer a.mu.Unlock()

		if a.current == state {
			a.current = nil
		}
	})
}
