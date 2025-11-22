package codex

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Store persists Codex session IDs for channels and threads.
type Store struct {
	path string

	mu       sync.Mutex
	Channels map[string]string `json:"channels"`
	Threads  map[string]string `json:"threads"`
}

// NewStore loads state from path or initializes an empty store.
func NewStore(path string) (*Store, error) {
	s := &Store{
		path:     path,
		Channels: make(map[string]string),
		Threads:  make(map[string]string),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[codex store] state file not found, starting new at %s", path)
			return s, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	log.Printf("[codex store] loaded state from %s (channels=%d threads=%d)", path, len(s.Channels), len(s.Threads))
	return s, nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	log.Printf("[codex store] saved state to %s", s.path)
	return nil
}

// GetChannel returns the session ID bound to a channel.
func (s *Store) GetChannel(channelID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Channels[channelID]
}

// SetChannel saves a session ID for a channel.
func (s *Store) SetChannel(channelID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Channels[channelID] = sessionID
	return s.saveLocked()
}

// DeleteChannel removes a session binding for a channel.
func (s *Store) DeleteChannel(channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Channels, channelID)
	return s.saveLocked()
}

// GetThread returns the session ID bound to a Discord thread.
func (s *Store) GetThread(threadID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Threads[threadID]
}

// SetThread saves a session ID for a thread.
func (s *Store) SetThread(threadID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Threads[threadID] = sessionID
	return s.saveLocked()
}

// DeleteThread removes a session binding for a thread.
func (s *Store) DeleteThread(threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Threads, threadID)
	return s.saveLocked()
}
