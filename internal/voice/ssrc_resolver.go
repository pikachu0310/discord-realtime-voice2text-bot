package voice

import (
	"sync"
	"time"
)

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
