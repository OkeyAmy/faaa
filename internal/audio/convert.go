package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/go-mp3"

	"github.com/okeyamy/faaa/internal/tools"
)

// maxInput caps decoding work for very long files (a 10 min track is plenty
// to find a hook in).
const maxInput = 10 * 60

// normalise downmixes to mono, resamples to SampleRate and, if longer than
// maxSec (0 = keep all), cuts the best-sounding window (see best.go).
// Returns the clip and where it starts in the source, in seconds.
func normalise(r *raw, maxSec float64) (PCM, float64) {
	ch := max(r.channels, 1)
	frames := min(len(r.samples)/ch, maxInput*max(r.rate, 1))
	if frames == 0 {
		return nil, 0
	}
	at := func(j int) float32 { // downmix one frame on the fly: no mono copy
		s := 0
		for c := range ch {
			s += int(r.samples[j*ch+c])
		}
		return float32(s) / float32(ch)
	}
	n := frames
	step := 1.0
	if r.rate != SampleRate && r.rate > 0 {
		n = int(int64(frames) * SampleRate / int64(r.rate))
		step = float64(r.rate) / SampleRate
	}
	rs := make([]float32, n) // linear-interpolation resample: fine for this
	for i := range rs {
		pos := float64(i) * step
		j := int(pos)
		if j >= frames-1 {
			rs[i] = at(frames - 1)
			continue
		}
		f := float32(pos - float64(j))
		rs[i] = at(j)*(1-f) + at(j+1)*f
	}
	r.samples = nil // let the decoded source go before analysis
	start := 0
	if win := int(maxSec * SampleRate); maxSec > 0 && n > win {
		start = BestStart(rs, SampleRate, maxSec)
		rs = rs[start : start+win]
	}
	out := make(PCM, len(rs))
	for i, v := range rs {
		out[i] = int16(v)
	}
	if start > 0 {
		fadeIn(out)
	}
	fadeOut(out)
	loudnorm(out)
	return append(make(PCM, int(LeadIn*SampleRate)), out...), float64(start) / SampleRate
}

// fadeIn: 20ms so a mid-track cut doesn't click.
func fadeIn(p PCM) {
	n := min(len(p), SampleRate/50)
	for i := range n {
		p[i] = int16(float64(p[i]) * float64(i) / float64(n))
	}
}

// LeadIn is silence prepended to every clip: laptop sound cards sleep when
// idle and swallow the first few hundred ms while waking up.
const LeadIn = 0.4

// loudnorm scales so the peak hits -1dBFS: meme clips vary wildly in level.
func loudnorm(p PCM) {
	peak := 1
	for _, v := range p {
		peak = max(peak, abs(int(v)))
	}
	g := 0.89 * 32767 / float64(peak)
	for i, v := range p {
		p[i] = int16(float64(v) * g)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// fadeOut applies a 50ms fade so trimmed clips don't click.
func fadeOut(p PCM) {
	n := min(len(p), SampleRate/20)
	for i := range n {
		k := len(p) - n + i
		p[k] = int16(float64(p[k]) * float64(n-i) / float64(n))
	}
}

func decodeMP3(r io.Reader) (raw, error) {
	d, err := mp3.NewDecoder(r)
	if err != nil {
		return raw{}, err
	}
	// stream-decode straight into int16: no intermediate byte slice
	s := make([]int16, 0, max(d.Length()/2, 0))
	buf := make([]byte, 32<<10)
	for {
		n, err := d.Read(buf)
		for i := 0; i+1 < n; i += 2 {
			s = append(s, int16(uint16(buf[i])|uint16(buf[i+1])<<8))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(s) == 0 {
				return raw{}, err
			}
			break
		}
	}
	return raw{samples: s, rate: d.SampleRate(), channels: 2}, nil
}

// Clip describes a converted sound (seconds).
type Clip struct {
	Len, Full, Start float64
}

// Convert decodes src (wav/mp3 natively, anything else via ffmpeg if
// installed), cuts the best maxSec window and writes a normalised WAV to dst.
func Convert(src, dst string, maxSec float64) (Clip, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return Clip{}, err
	}
	var r raw
	defer func() { b = nil }()
	ffOK := tools.Find("ffmpeg") != ""
	switch {
	case bytes.HasPrefix(b, []byte("RIFF")):
		r, err = readWAV(b)
	case len(b) > 1<<20 && ffOK:
		r, err = viaFFmpeg(src) // long track: native decoder, ~10x faster
	case isMP3(src, b):
		r, err = decodeMP3(bytes.NewReader(b))
	default:
		r, err = viaFFmpeg(src)
	}
	if err != nil {
		return Clip{}, fmt.Errorf("decode %s: %w", filepath.Base(src), err)
	}
	b = nil // encoded bytes no longer needed
	c := Clip{Full: float64(len(r.samples)/max(r.channels, 1)) / float64(max(r.rate, 1))}
	p, start := normalise(&r, maxSec)
	if len(p) <= int(LeadIn*SampleRate) {
		return c, errors.New("sound is empty")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return c, err
	}
	c.Len, c.Start = float64(len(p))/SampleRate-LeadIn, start
	tmp := dst + ".part"
	if err := WriteWAV(tmp, p); err != nil {
		return c, err
	}
	return c, os.Rename(tmp, dst) // atomic: a push never plays a half-written file
}

func isMP3(name string, b []byte) bool {
	if strings.EqualFold(filepath.Ext(name), ".mp3") || bytes.HasPrefix(b, []byte("ID3")) {
		return true
	}
	return len(b) > 1 && b[0] == 0xFF && b[1]&0xE0 == 0xE0
}

// viaFFmpeg handles m4a/aac/ogg/opus/webm etc. when ffmpeg is present.
func viaFFmpeg(src string) (raw, error) {
	ff := tools.Find("ffmpeg")
	if ff == "" {
		return raw{}, errors.New("unsupported format; use mp3/wav, or install ffmpeg")
	}
	out, err := exec.Command(ff, "-v", "error", "-i", src, "-t", fmt.Sprint(maxInput),
		"-f", "wav", "-acodec", "pcm_s16le", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-").Output()
	if err != nil {
		return raw{}, fmt.Errorf("ffmpeg: %w", err)
	}
	return readWAV(out)
}
