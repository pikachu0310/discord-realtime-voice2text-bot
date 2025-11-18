package audio

import (
	"context"
	"log"
	"math"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/pion/opus"
)

const (
	// SampleRate is the PCM sample rate used by Discord voice.
	SampleRate = 48000
	// Channels is the number of audio channels used during transcription.
	Channels     = 1
	frameSamples = 960 // 20ms at 48kHz
)

// Receiver consumes Discord Opus packets, decodes them to PCM, and feeds the segmenter.
type Receiver struct {
	segmenter    *Segmenter
	logger       *log.Logger
	userResolver func(uint32) (string, bool)
}

// NewReceiver creates a Receiver.
func NewReceiver(segmenter *Segmenter, resolver func(uint32) (string, bool)) *Receiver {
	if resolver == nil {
		resolver = func(uint32) (string, bool) {
			return "", false
		}
	}
	return &Receiver{
		segmenter:    segmenter,
		logger:       log.Default(),
		userResolver: resolver,
	}
}

// Start begins reading from the voice connection until ctx is done.
func (r *Receiver) Start(ctx context.Context, vc *discordgo.VoiceConnection) {
	if vc == nil {
		return
	}

	if vc.OpusRecv == nil {
		vc.OpusRecv = make(chan *discordgo.Packet, 512)
	}

	go r.consume(ctx, vc)
}

func (r *Receiver) consume(ctx context.Context, vc *discordgo.VoiceConnection) {
	defer r.segmenter.Stop()

	var (
		mu       sync.Mutex
		decoders = make(map[uint32]*opus.Decoder)
	)

	getDecoder := func(ssrc uint32) *opus.Decoder {
		mu.Lock()
		defer mu.Unlock()

		if dec, ok := decoders[ssrc]; ok {
			return dec
		}

		decoder := opus.NewDecoder()
		decoders[ssrc] = &decoder
		return &decoder
	}

	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-vc.OpusRecv:
			if !ok {
				return
			}
			if pkt == nil || len(pkt.Opus) == 0 {
				continue
			}
			userID, ok := r.userResolver(pkt.SSRC)
			if !ok || userID == "" {
				continue
			}
			decoder := getDecoder(pkt.SSRC)
			floatPCM := make([]float32, frameSamples)
			_, _, err := decoder.DecodeFloat32(pkt.Opus, floatPCM)
			if err != nil {
				r.logger.Printf("decode opus failed: %v", err)
				continue
			}
			r.segmenter.AddSamples(userID, float32ToPCM(floatPCM))
		}
	}
}

func float32ToPCM(src []float32) []int16 {
	dst := make([]int16, len(src))
	for i, sample := range src {
		if sample > 1 {
			sample = 1
		} else if sample < -1 {
			sample = -1
		}
		dst[i] = int16(math.Round(float64(sample * 32767)))
	}
	return dst
}
