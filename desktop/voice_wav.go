package main

import (
	"encoding/binary"
	"time"
)

const maxVoiceWAVBytes = 8 << 20

// Parse the entire bounded PCM16 mono WAV before trusting its duration．
func voiceWAVDuration(audio []byte, maxSeconds int) (time.Duration, bool) {
	if maxSeconds <= 0 || len(audio) < 44 || len(audio) > maxVoiceWAVBytes || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(audio[4:8]))+8 != uint64(len(audio)) {
		return 0, false
	}
	var sampleRate uint32
	var dataBytes int
	hasFormat, hasData := false, false
	for offset := 12; offset < len(audio); {
		if offset+8 > len(audio) {
			return 0, false
		}
		size := int(binary.LittleEndian.Uint32(audio[offset+4 : offset+8]))
		start := offset + 8
		if size > len(audio)-start {
			return 0, false
		}
		switch string(audio[offset : offset+4]) {
		case "fmt ":
			if hasFormat || size < 16 {
				return 0, false
			}
			format := audio[start : start+size]
			sampleRate = binary.LittleEndian.Uint32(format[4:8])
			if binary.LittleEndian.Uint16(format[:2]) != 1 || binary.LittleEndian.Uint16(format[2:4]) != 1 || binary.LittleEndian.Uint16(format[14:16]) != 16 || binary.LittleEndian.Uint16(format[12:14]) != 2 || sampleRate < 8000 || sampleRate > 48000 || binary.LittleEndian.Uint32(format[8:12]) != sampleRate*2 {
				return 0, false
			}
			hasFormat = true
		case "data":
			if hasData || size == 0 || size%2 != 0 {
				return 0, false
			}
			dataBytes, hasData = size, true
		}
		offset = start + size + size%2
		if offset > len(audio) {
			return 0, false
		}
	}
	if !hasFormat || !hasData || uint64(dataBytes) > uint64(sampleRate)*2*uint64(maxSeconds) {
		return 0, false
	}
	// Round up to avoid shortening playback by a fractional sample．
	frames := uint64(dataBytes / 2)
	duration := time.Duration((frames*uint64(time.Second) + uint64(sampleRate) - 1) / uint64(sampleRate))
	return duration, true
}

func validVoiceWAV(audio []byte, maxSeconds int) bool {
	_, valid := voiceWAVDuration(audio, maxSeconds)
	return valid
}
