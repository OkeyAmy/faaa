// Package audio decodes, trims and plays short sounds.
//
// Everything is normalised once to mono 16-bit PCM WAV at SampleRate so
// playback never decodes and any system player can handle the file.
package audio

import (
	"encoding/binary"
	"errors"
	"os"
)

const SampleRate = 22050

// PCM is mono int16 samples at SampleRate.
type PCM []int16

// WriteWAV writes mono 16-bit PCM.
func WriteWAV(path string, p PCM) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data := uint32(len(p) * 2)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], SampleRate)
	binary.LittleEndian.PutUint32(h[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	if _, err := f.Write(h); err != nil {
		return err
	}
	return binary.Write(f, binary.LittleEndian, []int16(p))
}

// raw is interleaved int16 audio of any rate/channel count.
type raw struct {
	samples  []int16
	rate     int
	channels int
}

// readWAV parses 8/16-bit PCM WAV (any rate, any channels).
func readWAV(b []byte) (raw, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return raw{}, errors.New("not a WAV file")
	}
	var out raw
	bits := 0
	for i := 12; i+8 <= len(b); {
		id, size := string(b[i:i+4]), int(binary.LittleEndian.Uint32(b[i+4:]))
		body := b[i+8 : min(i+8+size, len(b))]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return raw{}, errors.New("bad fmt chunk")
			}
			if f := binary.LittleEndian.Uint16(body); f != 1 && f != 0xFFFE {
				return raw{}, errors.New("unsupported WAV encoding (need PCM)")
			}
			out.channels = int(binary.LittleEndian.Uint16(body[2:]))
			out.rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			switch bits {
			case 16:
				out.samples = make([]int16, len(body)/2)
				for j := range out.samples {
					out.samples[j] = int16(binary.LittleEndian.Uint16(body[j*2:]))
				}
			case 8:
				out.samples = make([]int16, len(body))
				for j, v := range body {
					out.samples[j] = (int16(v) - 128) << 8
				}
			default:
				return raw{}, errors.New("unsupported WAV bit depth")
			}
		}
		i += 8 + size + size%2
	}
	if out.channels == 0 || out.samples == nil {
		return raw{}, errors.New("WAV missing fmt or data")
	}
	return out, nil
}

// ReadPCM loads a normalised WAV written by WriteWAV.
func ReadPCM(path string) (PCM, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := readWAV(b)
	if err != nil {
		return nil, err
	}
	return PCM(r.samples), nil // already normalised by WriteWAV's callers
}
