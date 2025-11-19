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
	// waitForMappingTimeout defines how long to wait for SSRC mapping before dropping a packet.
	waitForMappingTimeout = 2 * time.Second
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
}

// NewReceiver creates a Receiver.
func NewReceiver(segmenter *Segmenter, resolver SSRCResolver) *Receiver {
	return &Receiver{
		segmenter:   segmenter,
		logger:      log.Default(),
		resolver:    resolver,
		unknownSSRC: make(map[uint32]struct{}),
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

	userID := pkt.UserID
	if userID == "" && r.resolver != nil {
		if resolved, ok := r.resolver.Resolve(pkt.SSRC); ok {
			userID = resolved
		}
	}

	if userID == "" {
		r.deferPacket(pkt, getDecoder)
		return
	}

	r.decodeAndDispatch(userID, pkt.Opus, pkt.SSRC, getDecoder)
}

func (r *Receiver) deferPacket(pkt *discordgo.Packet, getDecoder func(uint32) (*gopus.Decoder, error)) {
	if r.resolver == nil {
		r.logUnknownSSRC(pkt.SSRC)
		return
	}

	payload := append([]byte(nil), pkt.Opus...)
	ssrc := pkt.SSRC

	go func() {
		userID, ok := r.resolver.Wait(ssrc, waitForMappingTimeout)
		if !ok || userID == "" {
			r.logUnknownSSRC(ssrc)
			return
		}
		r.decodeAndDispatch(userID, payload, ssrc, getDecoder)
	}()
}

func (r *Receiver) decodeAndDispatch(userID string, opusFrame []byte, ssrc uint32, getDecoder func(uint32) (*gopus.Decoder, error)) {
	decoder, err := getDecoder(ssrc)
	if err != nil {
		r.logger.Printf("create decoder failed: %v", err)
		return
	}

	pcm, err := decoder.Decode(opusFrame, frameSamples, false)
	if err != nil {
		r.logger.Printf("decode opus failed: %v", err)
		return
	}

	r.segmenter.AddSamples(userID, pcm)
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
