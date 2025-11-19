package transcript

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const maxDiscordMessageLength = 2000

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
	id      string
	lines   []string
	content string
	timer   *time.Timer
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

	for _, chunk := range splitLine(line) {
		if a.current == nil {
			if err := a.startNewMessageLocked(chunk); err != nil {
				return err
			}
			continue
		}

		if a.wouldExceedLimitLocked(chunk) {
			a.finalizeCurrentLocked()
			if err := a.startNewMessageLocked(chunk); err != nil {
				return err
			}
			continue
		}

		if err := a.appendToCurrentLocked(chunk); err != nil {
			return err
		}
	}
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

func (a *Aggregator) startNewMessageLocked(line string) error {
	msgID, err := a.poster.SendMessage(a.channelID, line)
	if err != nil {
		return err
	}
	state := &messageState{
		id:      msgID,
		lines:   []string{line},
		content: line,
	}
	a.current = state
	a.resetTimerLocked(state)
	return nil
}

func (a *Aggregator) appendToCurrentLocked(line string) error {
	newContent := line
	if a.current.content != "" {
		newContent = strings.Join([]string{a.current.content, line}, "\n")
	}
	if err := a.poster.EditMessage(a.channelID, a.current.id, newContent); err != nil {
		return err
	}
	a.current.lines = append(a.current.lines, line)
	a.current.content = newContent
	a.resetTimerLocked(a.current)
	return nil
}

func (a *Aggregator) finalizeCurrentLocked() {
	if a.current == nil {
		return
	}
	if a.current.timer != nil {
		a.current.timer.Stop()
	}
	a.current = nil
}

func (a *Aggregator) wouldExceedLimitLocked(line string) bool {
	if a.current == nil {
		return len(line) > maxDiscordMessageLength
	}
	extra := len(line)
	if a.current.content != "" {
		extra++ // newline
	}
	return len(a.current.content)+extra > maxDiscordMessageLength
}

func splitLine(line string) []string {
	runes := []rune(line)
	if len(runes) <= maxDiscordMessageLength {
		return []string{line}
	}
	var parts []string
	for len(runes) > 0 {
		n := maxDiscordMessageLength
		if n > len(runes) {
			n = len(runes)
		}
		parts = append(parts, string(runes[:n]))
		runes = runes[n:]
	}
	return parts
}
