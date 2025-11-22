package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// Client runs the Codex CLI to continue or start a session.
type Client struct {
	Model           string
	ReasoningEffort string
}

// Send executes `codex exec` (or `exec resume`) and returns the assistant reply text and session ID.
// If sessionID is empty, a new session is started.
// The onUpdate callback receives short progress summaries as the CLI streams events.
func (c Client) Send(ctx context.Context, sessionID, prompt string, onUpdate func(string)) (string, string, error) {
	args := []string{"exec"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.ReasoningEffort != "" {
		args = append(args, "-c", fmt.Sprintf("reasoning.effort=%s", c.ReasoningEffort))
	}
	args = append(args,
		"--json",
		"--color", "never",
		"--dangerously-bypass-approvals-and-sandbox",
	)
	if sessionID != "" {
		args = append(args, "resume", sessionID)
	}
	args = append(args, prompt)

	log.Printf("[codex] exec args=%v session=%s prompt_preview=%s", args, sessionID, preview(prompt))

	reply, newSession, err := c.runOnce(ctx, args, sessionID, onUpdate)
	if err == nil {
		return reply, newSession, nil
	}

	if c.Model != "" {
		if newSession != "" {
			sessionID = newSession
		}
		if onUpdate != nil {
			onUpdate(fmt.Sprintf("⚠️ モデル %s で失敗したためデフォルト設定で再試行します: %v", c.Model, err))
		}
		noModel := filterModelArgs(args)
		log.Printf("[codex] retrying without model after failure: %v (args=%v)", err, noModel)
		reply2, session2, err2 := c.runOnce(ctx, noModel, sessionID, onUpdate)
		if err2 == nil {
			return reply2, session2, nil
		}
		return reply2, session2, fmt.Errorf("fallback without model also failed: %w", err2)
	}
	return reply, newSession, err
}

type codexEvent struct {
	Type      string             `json:"type"`
	Session   *codexEventSession `json:"session,omitempty"`
	SessionID string             `json:"session_id,omitempty"`
	ThreadID  string             `json:"thread_id,omitempty"`
	Item      *codexEventItem    `json:"item,omitempty"`
	Result    string             `json:"result,omitempty"`
	Response  *struct {
		OutputText any `json:"output_text,omitempty"`
	} `json:"response,omitempty"`
	Error *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type codexEventSession struct {
	ID string `json:"id,omitempty"`
}

type codexEventItem struct {
	Text       string `json:"text,omitempty"`
	Delta      any    `json:"delta,omitempty"`
	OutputText any    `json:"output_text,omitempty"`
	Content    any    `json:"content,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Message    string `json:"message,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Type       string `json:"type,omitempty"`
	Command    string `json:"command,omitempty"`
}

func parseCodexJSONLines(output string, onUpdate func(string)) (string, string) {
	var (
		textBuilder strings.Builder
		sessionID   string
		agentMsg    string
	)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt codexEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			// fallback: not JSON, append raw line
			textBuilder.WriteString(line)
			textBuilder.WriteString("\n")
			continue
		}
		if sid := evt.SessionID; sid != "" {
			sessionID = sid
		}
		if evt.Session != nil && evt.Session.ID != "" {
			sessionID = evt.Session.ID
		}
		if evt.Item != nil && evt.Item.SessionID != "" {
			sessionID = evt.Item.SessionID
		}
		if evt.ThreadID != "" {
			sessionID = evt.ThreadID
		}

		if onUpdate != nil {
			if summary := summarizeEvent(evt); summary != "" {
				onUpdate(summary)
			}
		}

		if evt.Item != nil && evt.Item.Type == "agent_message" && strings.TrimSpace(evt.Item.Text) != "" {
			agentMsg = evt.Item.Text
		}
		textBuilder.WriteString(extractText(evt))
	}
	if strings.TrimSpace(agentMsg) != "" {
		return strings.TrimSpace(agentMsg), sessionID
	}
	return strings.TrimSpace(textBuilder.String()), sessionID
}

func summarizeEvent(evt codexEvent) string {
	if evt.Type == "" || evt.Type != "item.completed" || evt.Item == nil {
		return ""
	}
	switch evt.Item.Type {
	case "reasoning":
		if strings.TrimSpace(evt.Item.Text) == "" {
			return ""
		}
		return fmt.Sprintf(":thinking: %s", strings.TrimSpace(evt.Item.Text))
	case "command_execution":
		if strings.TrimSpace(evt.Item.Command) == "" {
			return ""
		}
		return fmt.Sprintf(":computer: `%s`", strings.TrimSpace(evt.Item.Command))
	case "agent_message":
		return ""
	default:
		detail := eventDetail(evt)
		if detail == "" {
			return ""
		}
		return fmt.Sprintf(":thinking: %s", detail)
	}
}

func preview(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	runes := []rune(text)
	if len(runes) > 120 {
		return string(runes[:120]) + "..."
	}
	return text
}

func eventDetail(evt codexEvent) string {
	candidates := []string{
		evt.Result,
	}
	if evt.Item != nil {
		candidates = append(candidates,
			evt.Item.Text,
			evt.Item.Message,
			anyToString(evt.Item.Delta),
			anyToString(evt.Item.OutputText),
		)
	}
	if evt.Error != nil {
		candidates = append(candidates, evt.Error.Message)
	}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		return preview(strings.TrimSpace(c))
	}
	return ""
}

func filterModelArgs(args []string) []string {
	out := make([]string, 0, len(args))
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if a == "--model" {
			skip = true
			continue
		}
		out = append(out, a)
	}
	return out
}

func (c Client) runOnce(ctx context.Context, args []string, sessionID string, onUpdate func(string)) (string, string, error) {
	cmd := exec.CommandContext(ctx, "codex", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	outStr := stdout.String()
	parsedText, discoveredSession := parseCodexJSONLines(outStr, onUpdate)
	if parsedText == "" {
		parsedText = strings.TrimSpace(outStr)
	}
	if discoveredSession != "" {
		sessionID = discoveredSession
	}
	if runErr != nil {
		errText := strings.TrimSpace(stderr.String())
		log.Printf("[codex] exec failed code=%v stderr=%s output_preview=%s", runErr, errText, preview(parsedText))
		return parsedText, sessionID, fmt.Errorf("codex exec failed: %w (stderr=%s output=%s)", runErr, errText, preview(parsedText))
	}
	if parsedText == "" {
		return "", sessionID, errors.New("codex returned empty response")
	}
	log.Printf("[codex] response session=%s text_preview=%s", sessionID, preview(parsedText))
	return parsedText, sessionID, nil
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if txt, ok := t["text"]; ok {
			return anyToString(txt)
		}
		if delta, ok := t["text_delta"]; ok {
			return anyToString(delta)
		}
	case []any:
		if len(t) > 0 {
			return anyToString(t[0])
		}
	}
	return ""
}

func extractText(evt codexEvent) string {
	var b strings.Builder

	if evt.Item != nil && evt.Item.Type == "agent_message" {
		appendTextLike(&b, evt.Item.Text)
		appendAnyText(&b, evt.Item.OutputText)
		appendAnyText(&b, evt.Item.Content)
		appendTextLike(&b, evt.Item.Message)
	}
	return b.String()
}

func appendTextLike(b *strings.Builder, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(text)
}

func appendAnyText(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		appendTextLike(b, v)
	case []any:
		for _, item := range v {
			appendAnyText(b, item)
		}
	case map[string]any:
		if txt, ok := v["text"]; ok {
			appendAnyText(b, txt)
		}
		if delta, ok := v["text_delta"]; ok {
			appendAnyText(b, delta)
		}
	}
}
