package audio

import (
	"sync"
	"time"
)

// SegmentConsumer is invoked when a user's audio segment is ready to process.
type SegmentConsumer func(guildID, userID string, samples []int16)

// Segmenter groups PCM samples into per-user segments with a silence timeout.
type Segmenter struct {
	guildID  string
	timeout  time.Duration
	consumer SegmentConsumer

	mu      sync.Mutex
	buffers map[string]*userBuffer
}

type userBuffer struct {
	samples []int16
	timer   *time.Timer
}

// NewSegmenter returns a new Segmenter.
func NewSegmenter(guildID string, silence time.Duration, consumer SegmentConsumer) *Segmenter {
	return &Segmenter{
		guildID:  guildID,
		timeout:  silence,
		consumer: consumer,
		buffers:  make(map[string]*userBuffer),
	}
}

// AddSamples appends new PCM samples for the given user and schedules flush on silence.
func (s *Segmenter) AddSamples(userID string, samples []int16) {
	if len(samples) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	buf := s.buffers[userID]
	if buf == nil {
		buf = &userBuffer{}
		s.buffers[userID] = buf
	}

	buf.samples = append(buf.samples, samples...)
	s.resetTimerLocked(userID, buf)
}

// Stop flushes all active buffers.
func (s *Segmenter) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for userID, buf := range s.buffers {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		s.flushLocked(userID, buf)
		delete(s.buffers, userID)
	}
}

func (s *Segmenter) resetTimerLocked(userID string, buf *userBuffer) {
	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(s.timeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		current := s.buffers[userID]
		if current != buf {
			return
		}
		s.flushLocked(userID, buf)
	})
}

func (s *Segmenter) flushLocked(userID string, buf *userBuffer) {
	if len(buf.samples) == 0 {
		return
	}
	cp := make([]int16, len(buf.samples))
	copy(cp, buf.samples)
	buf.samples = buf.samples[:0]

	go s.consumer(s.guildID, userID, cp)
}
