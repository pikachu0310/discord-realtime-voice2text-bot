package transcript

import (
	"testing"
	"time"
)

type mockPoster struct {
	sentMessages  []string
	editedContent []string
}

func (m *mockPoster) SendMessage(channelID, content string) (string, error) {
	m.sentMessages = append(m.sentMessages, content)
	return "msg-id", nil
}

func (m *mockPoster) EditMessage(channelID, messageID, content string) error {
	m.editedContent = append(m.editedContent, content)
	return nil
}

func TestAggregatorAddLine(t *testing.T) {
	poster := &mockPoster{}
	agg := NewAggregator("chan", poster, 20*time.Millisecond)

	if err := agg.AddLine("first"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(poster.sentMessages) != 1 {
		t.Fatalf("expected 1 send, got %d", len(poster.sentMessages))
	}

	if err := agg.AddLine("second"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(poster.editedContent) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(poster.editedContent))
	}

	time.Sleep(40 * time.Millisecond)

	if err := agg.AddLine("third"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(poster.sentMessages) != 2 {
		t.Fatalf("expected new send after timeout, got %d", len(poster.sentMessages))
	}
}
