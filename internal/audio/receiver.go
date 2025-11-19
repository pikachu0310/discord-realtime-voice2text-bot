package audio

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"layeh.com/gopus"
)

const (
	// SampleRate is the PCM sample rate used by Discord voice.
	SampleRate = 48000
	// Channels is the number of audio channels used during transcription.
	Channels     = 1
	frameSamples = 960 // 20ms at 48kHz
	// waitForMappingTimeout defines how long to wait for SSRC mapping before dropping buffered audio.
	waitForMappingTimeout = 5 * time.Minute
	maxPendingDuration    = 30 * time.Second
	maxPendingSamples     = SampleRate * Channels * int(maxPendingDuration/time.Second)
)

// SSRCResolver resolves SSRC values to Discord user IDs.
type SSRCResolver interface {
	Resolve(ssrc uint32) (string, bool)
	Wait(ssrc uint32, timeout time.Duration) (string, bool)
}

// Receiver consumes Discord Opus packets, decodes them to PCM, and feeds the segmenter.
type Receiver struct {
	segmenter *Segmenter
	logger    *log.Logger
	resolver  SSRCResolver

	unknownSSRC map[uint32]struct{}
	mu          sync.Mutex

	pendingMu sync.Mutex
	pending   map[uint32]*pendingStream
}

type pendingStream struct {
	frames       [][]int16
	totalSamples int
	waiting      bool
}

// NewReceiver creates a Receiver.
func NewReceiver(segmenter *Segmenter, resolver SSRCResolver) *Receiver {
	return &Receiver{
		segmenter:   segmenter,
		logger:      log.Default(),
		resolver:    resolver,
		unknownSSRC: make(map[uint32]struct{}),
		pending:     make(map[uint32]*pendingStream),
	}
}

// Start begins reading from the voice connection until ctx is done.
func (r *Receiver) Start(ctx context.Context, vc *discordgo.VoiceConnection) {
	if vc == nil {
		return
	}

	if err := r.waitForOpusChannel(ctx, vc); err != nil {
		r.logger.Printf("voice receiver aborted: %v", err)
		return
	}

	go r.consume(ctx, vc)
}

func (r *Receiver) waitForOpusChannel(ctx context.Context, vc *discordgo.VoiceConnection) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	for {
		if vc.OpusRecv != nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("voice connection did not expose OpusRecv channel in time")
		case <-ticker.C:
		}
	}
}

func (r *Receiver) consume(ctx context.Context, vc *discordgo.VoiceConnection) {
	defer r.segmenter.Stop()

	var (
		mu       sync.Mutex
		decoders = make(map[uint32]*gopus.Decoder)
	)

	getDecoder := func(ssrc uint32) (*gopus.Decoder, error) {
		mu.Lock()
		defer mu.Unlock()

		if dec, ok := decoders[ssrc]; ok {
			return dec, nil
		}

		decoder, err := gopus.NewDecoder(SampleRate, Channels)
		if err != nil {
			return nil, err
		}
		decoders[ssrc] = decoder
		return decoder, nil
	}

	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-vc.OpusRecv:
			if !ok {
				return
			}
			r.handlePacket(pkt, getDecoder)
		}
	}
}

func (r *Receiver) handlePacket(pkt *discordgo.Packet, getDecoder func(uint32) (*gopus.Decoder, error)) {
	if pkt == nil || len(pkt.Opus) == 0 {
		return
	}

	userID := r.resolveImmediate(pkt.SSRC, pkt.UserID)
	pcm := r.decodeFrame(pkt.SSRC, pkt.Opus, getDecoder)
	if len(pcm) == 0 {
		return
	}

	if userID == "" {
		r.logger.Printf("opcode recv: buffering frame ssrc=%d seq=%d timestamp=%d (no mapping yet)", pkt.SSRC, pkt.Sequence, pkt.Timestamp)
		r.bufferPending(pkt.SSRC, pcm, getDecoder)
		return
	}

	r.logger.Printf("opcode recv: resolved user=%s ssrc=%d seq=%d samples=%d", userID, pkt.SSRC, pkt.Sequence, len(pcm))

	r.segmenter.AddSamples(userID, pcm)
}

func (r *Receiver) resolveImmediate(ssrc uint32, initial string) string {
	if initial != "" {
		return initial
	}
	if r.resolver == nil {
		return ""
	}
	if userID, ok := r.resolver.Resolve(ssrc); ok {
		return userID
	}
	return ""
}

func (r *Receiver) decodeFrame(ssrc uint32, opusFrame []byte, getDecoder func(uint32) (*gopus.Decoder, error)) []int16 {
	decoder, err := getDecoder(ssrc)
	if err != nil {
		r.logger.Printf("create decoder failed: %v", err)
		return nil
	}

	pcm, err := decoder.Decode(opusFrame, frameSamples, false)
	if err != nil {
		r.logger.Printf("decode opus failed: %v", err)
		return nil
	}
	return pcm
}

func (r *Receiver) bufferPending(ssrc uint32, pcm []int16, getDecoder func(uint32) (*gopus.Decoder, error)) {
	if r.resolver == nil {
		r.logUnknownSSRC(ssrc)
		return
	}

	startWait, totalSamples, frameCount := r.addPendingFrame(ssrc, pcm)
	r.logger.Printf("pending buffer: ssrc=%d frames=%d total_samples=%d waiting=%t", ssrc, frameCount, totalSamples, startWait)
	if startWait {
		go r.awaitMapping(ssrc)
	}
}

func (r *Receiver) addPendingFrame(ssrc uint32, pcm []int16) (bool, int, int) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	stream := r.pending[ssrc]
	if stream == nil {
		stream = &pendingStream{}
		r.pending[ssrc] = stream
	}
	stream.frames = append(stream.frames, pcm)
	stream.totalSamples += len(pcm)
	for stream.totalSamples > maxPendingSamples && len(stream.frames) > 0 {
		removed := len(stream.frames[0])
		stream.frames = stream.frames[1:]
		stream.totalSamples -= removed
	}
	if stream.waiting {
		return false, stream.totalSamples, len(stream.frames)
	}
	stream.waiting = true
	return true, stream.totalSamples, len(stream.frames)
}

func (r *Receiver) awaitMapping(ssrc uint32) {
	if r.resolver == nil {
		return
	}
	r.logger.Printf("await mapping start: ssrc=%d", ssrc)
	userID, ok := r.resolver.Wait(ssrc, waitForMappingTimeout)
	if !ok || userID == "" {
		r.logger.Printf("await mapping failed: ssrc=%d", ssrc)
		r.logUnknownSSRC(ssrc)
		r.clearPending(ssrc)
		return
	}
	frames := r.drainPending(ssrc)
	r.logger.Printf("await mapping success: ssrc=%d user=%s frames=%d", ssrc, userID, len(frames))
	for _, frame := range frames {
		r.segmenter.AddSamples(userID, frame)
	}
}

func (r *Receiver) drainPending(ssrc uint32) [][]int16 {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	stream := r.pending[ssrc]
	if stream == nil {
		return nil
	}
	delete(r.pending, ssrc)
	return stream.frames
}

func (r *Receiver) clearPending(ssrc uint32) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	delete(r.pending, ssrc)
}

func (r *Receiver) logUnknownSSRC(ssrc uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, logged := r.unknownSSRC[ssrc]; logged {
		return
	}
	r.unknownSSRC[ssrc] = struct{}{}
	r.logger.Printf("no user mapping for SSRC=%d yet", ssrc)
}
