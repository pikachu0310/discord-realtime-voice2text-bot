package audio

import (
	"encoding/binary"
	"fmt"
	"os"
)

// WritePCM16ToWAV writes the provided PCM samples into a signed 16-bit mono/stereo WAV file.
func WritePCM16ToWAV(path string, samples []int16, sampleRate, channels int) error {
	if channels <= 0 {
		return fmt.Errorf("channels must be positive")
	}
	if sampleRate <= 0 {
		return fmt.Errorf("sample rate must be positive")
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create wav file: %w", err)
	}
	defer file.Close()

	byteRate := sampleRate * channels * 2
	blockAlign := channels * 2
	dataLen := len(samples) * 2
	chunkSize := 36 + dataLen

	// RIFF header
	if _, err := file.Write([]byte("RIFF")); err != nil {
		return fmt.Errorf("write riff header: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(chunkSize)); err != nil {
		return fmt.Errorf("write chunk size: %w", err)
	}
	if _, err := file.Write([]byte("WAVEfmt ")); err != nil {
		return fmt.Errorf("write wave fmt: %w", err)
	}

	if err := binary.Write(file, binary.LittleEndian, uint32(16)); err != nil {
		return fmt.Errorf("write subchunk size: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint16(1)); err != nil { // PCM format
		return fmt.Errorf("write audio format: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint16(channels)); err != nil {
		return fmt.Errorf("write channels: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return fmt.Errorf("write sample rate: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(byteRate)); err != nil {
		return fmt.Errorf("write byte rate: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint16(blockAlign)); err != nil {
		return fmt.Errorf("write block align: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint16(16)); err != nil { // bits per sample
		return fmt.Errorf("write bits per sample: %w", err)
	}

	if _, err := file.Write([]byte("data")); err != nil {
		return fmt.Errorf("write data header: %w", err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(dataLen)); err != nil {
		return fmt.Errorf("write data length: %w", err)
	}

	for _, sample := range samples {
		if err := binary.Write(file, binary.LittleEndian, sample); err != nil {
			return fmt.Errorf("write sample: %w", err)
		}
	}

	return nil
}
