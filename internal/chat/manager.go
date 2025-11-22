package chat

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/pikachu0310/whisper-discord-bot/internal/codex"
)

const (
	commandNameChat   = "chat"
	commandNameStart  = "start"
	commandNameReset  = "reset"
	commandNameThread = "thread"

	threadArchiveMinutes = 1440
)

// Manager handles Codex-powered conversations in channels and threads.
type Manager struct {
	session *discordgo.Session
	codex   codex.Client
	store   *codex.Store
	namer   *codex.ThreadNamer

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewManager creates a chat manager.
func NewManager(session *discordgo.Session, store *codex.Store, namer *codex.ThreadNamer, client codex.Client) *Manager {
	return &Manager{
		session: session,
		codex:   client,
		store:   store,
		namer:   namer,
		locks:   make(map[string]*sync.Mutex),
	}
}

// RegisterCommands registers slash commands globally.
func (m *Manager) RegisterCommands() error {
	if m.session == nil || m.session.State == nil || m.session.State.User == nil {
		return fmt.Errorf("discord session is not ready yet")
	}
	appID := m.session.State.User.ID
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        commandNameChat,
			Description: "Codex とチャンネル内で会話します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "送信するメッセージ",
					Required:    true,
				},
			},
		},
		{
			Name:        commandNameStart,
			Description: "Codex とチャンネル内で会話を開始します（/chat のエイリアス）",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "送信するメッセージ",
					Required:    true,
				},
			},
		},
		{
			Name:        commandNameReset,
			Description: "このチャンネルの Codex セッションをリセットします",
		},
		{
			Name:        commandNameThread,
			Description: "新しい Discord スレッドで Codex と会話を開始します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "スレッド開始メッセージ",
					Required:    true,
				},
			},
		},
	}

	for _, cmd := range commands {
		if _, err := m.session.ApplicationCommandCreate(appID, "", cmd); err != nil {
			return err
		}
	}
	return nil
}

// HandleInteraction routes slash commands.
func (m *Manager) HandleInteraction(ic *discordgo.InteractionCreate) {
	if ic == nil || ic.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := ic.ApplicationCommandData()

	switch data.Name {
	case commandNameChat, commandNameStart:
		if len(data.Options) == 0 {
			return
		}
		msg := strings.TrimSpace(data.Options[0].StringValue())
		if msg == "" {
			m.followup(ic, "空のメッセージは送信できません。", true)
			return
		}
		go m.handleChatCommand(ic, msg)
	case commandNameReset:
		go m.handleResetCommand(ic)
	case commandNameThread:
		if len(data.Options) == 0 {
			return
		}
		msg := strings.TrimSpace(data.Options[0].StringValue())
		if msg == "" {
			m.followup(ic, "空のメッセージは送信できません。", true)
			return
		}
		go m.handleThreadCommand(ic, msg)
	}
}

// HandleThreadMessage handles messages inside managed threads.
// Messages in threads with a stored Codex session will be forwarded without commands.
func (m *Manager) HandleThreadMessage(msg *discordgo.MessageCreate) {
	if msg == nil || msg.Author.Bot {
		return
	}
	threadSession := m.store.GetThread(msg.ChannelID)
	if threadSession == "" {
		return
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return
	}

	progress := newProgress("Codex").WithInput(content)
	sent, err := m.session.ChannelMessageSend(msg.ChannelID, progress.Render())
	if err == nil && sent != nil {
		progress.OnUpdate = func(text string) {
			m.session.ChannelMessageEdit(msg.ChannelID, sent.ID, text)
		}
	}

	go m.sendAndReply(msg.ChannelID, threadSession, content, progress, func(newSessionID string) {
		effective := newSessionID
		if effective == "" {
			effective = threadSession
		}
		if effective != "" && effective != threadSession {
			if err := m.store.SetThread(msg.ChannelID, effective); err != nil {
				log.Printf("failed to update thread session: %v", err)
			}
		}
	})
}

// ChatInChannel is a helper (unused by slash path) that returns rendered progress text.
func (m *Manager) ChatInChannel(channelID, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("メッセージが空です")
	}
	sessionID := m.store.GetChannel(channelID)

	progress := newProgress("Codex").WithInput(content)
	var final string
	progress.OnUpdate = func(text string) {
		final = text
	}

	if err := m.sendAndReply(channelID, sessionID, content, progress, func(newSessionID string) {
		effective := newSessionID
		if effective == "" {
			effective = sessionID
		}
		if effective == "" {
			return
		}
		if err := m.store.SetChannel(channelID, effective); err != nil {
			log.Printf("failed to persist channel session: %v", err)
		}
	}); err != nil {
		return "", err
	}
	if final == "" {
		return "", fmt.Errorf("返信が取得できませんでした")
	}
	return final, nil
}

func (m *Manager) handleChatCommand(ic *discordgo.InteractionCreate, content string) {
	// acknowledge quickly
	if err := m.session.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Codex に送信中です…",
		},
	}); err != nil {
		log.Printf("interaction respond failed: %v", err)
		return
	}

	channelID := ic.ChannelID
	sessionID := m.store.GetChannel(channelID)
	progress := newProgress("Codex").WithInput(content)

	progress.OnUpdate = func(text string) {
		_, err := m.session.InteractionResponseEdit(ic.Interaction, &discordgo.WebhookEdit{
			Content: &text,
		})
		if err != nil {
			log.Printf("failed to edit interaction response: %v", err)
		}
	}

	// ensure first render shows immediately
	progress.OnUpdate(progress.Render())

	_ = m.sendAndReply(channelID, sessionID, content, progress, func(newSessionID string) {
		effective := newSessionID
		if effective == "" {
			effective = sessionID
		}
		if effective == "" {
			return
		}
		if err := m.store.SetChannel(channelID, effective); err != nil {
			log.Printf("failed to persist channel session: %v", err)
		}
	})
}

func (m *Manager) handleResetCommand(ic *discordgo.InteractionCreate) {
	channelID := ic.ChannelID
	if err := m.store.DeleteChannel(channelID); err != nil {
		log.Printf("reset failed: %v", err)
	}
	m.followup(ic, "このチャンネルの会話履歴をリセットしました。次の /chat から新規セッションになります。", true)
}

func (m *Manager) handleThreadCommand(ic *discordgo.InteractionCreate, content string) {
	if ic.ChannelID == "" {
		return
	}
	if err := m.session.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "スレッドを作成しています…",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("interaction respond failed: %v", err)
		return
	}

	desiredName := m.namer.Generate(context.Background(), content, "")
	thread, err := m.session.ThreadStartComplex(ic.ChannelID, &discordgo.ThreadStart{
		Name:                desiredName,
		AutoArchiveDuration: threadArchiveMinutes,
		Type:                discordgo.ChannelTypeGuildPublicThread,
	})
	if err != nil {
		log.Printf("failed to create thread: %v", err)
		m.followup(ic, fmt.Sprintf("スレッドの作成に失敗しました: %v", err), true)
		return
	}

	// Rename even when Gemini is set to ensure final name is applied.
	finalName := m.namer.Generate(context.Background(), content, thread.ID)
	if finalName != "" && finalName != thread.Name {
		if _, err := m.session.ChannelEdit(thread.ID, &discordgo.ChannelEdit{Name: finalName}); err != nil {
			log.Printf("failed to rename thread: %v", err)
		}
	}

	m.followup(ic, fmt.Sprintf("スレッドを作成しました: <#%s>", thread.ID), true)

	if _, err := m.session.ChannelMessageSend(thread.ID, fmt.Sprintf("スレッド開始: %s", content)); err != nil {
		log.Printf("failed to send initial thread message: %v", err)
	}

	progress := newProgress("Codex").WithInput(content)
	msg, err := m.session.ChannelMessageSend(thread.ID, progress.Render())
	if err == nil && msg != nil {
		progress.OnUpdate = func(text string) {
			m.session.ChannelMessageEdit(thread.ID, msg.ID, text)
		}
	}

	_ = m.sendAndReply(thread.ID, m.store.GetThread(thread.ID), content, progress, func(newSessionID string) {
		effective := newSessionID
		if effective == "" {
			effective = m.store.GetThread(thread.ID)
		}
		if effective == "" {
			return
		}
		if err := m.store.SetThread(thread.ID, effective); err != nil {
			log.Printf("failed to persist thread session: %v", err)
		}
	})
}

func (m *Manager) sendAndReply(targetID, sessionID, content string, progress *progressBuilder, persist func(string)) error {
	lock := m.getLock(targetID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if progress == nil {
		progress = newProgress("Codex")
	}

	progress.AddStep(fmt.Sprintf("🚀 実行開始 session=%s model=%s", sessionID, m.codex.Model))
	if progress.OnUpdate != nil {
		progress.OnUpdate(progress.Render())
	}

	update := func(line string) {
		progress.AddStep(line)
		if progress.OnUpdate != nil {
			progress.OnUpdate(progress.Render())
		}
	}

	log.Printf("[chat] send start target=%s session=%s len(content)=%d", targetID, sessionID, len(content))

	reply, newSessionID, err := m.codex.Send(ctx, sessionID, content, update)
	if err != nil {
		log.Printf("codex send failed: %v", err)
		if progress.OnUpdate != nil {
			progress.OnUpdate(fmt.Sprintf("⚠️ Codex への送信に失敗しました: %v", err))
		}
		return err
	}
	progress.SetFinal("🧠 " + reply)
	if progress.OnUpdate != nil {
		progress.OnUpdate(progress.Render())
	}

	effectiveSession := newSessionID
	if effectiveSession == "" {
		effectiveSession = sessionID
	}
	if persist != nil && effectiveSession != "" {
		persist(effectiveSession)
	}
	log.Printf("[chat] send done target=%s session=%s", targetID, effectiveSession)
	return nil
}

func (m *Manager) getLock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lk, ok := m.locks[id]; ok {
		return lk
	}
	lk := &sync.Mutex{}
	m.locks[id] = lk
	return lk
}

func (m *Manager) followup(ic *discordgo.InteractionCreate, content string, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	_, err := m.session.FollowupMessageCreate(ic.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Flags:   flags,
	})
	if err != nil {
		log.Printf("failed to send followup: %v", err)
	}
}

// Close is a no-op placeholder for future cleanup hooks.
func (m *Manager) Close() {}
