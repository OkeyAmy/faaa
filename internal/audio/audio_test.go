package audio

import (
	"math"
	"path/filepath"
	"testing"
)

func TestNormaliseTrimsDownmixesResamples(t *testing.T) {
	// 20s stereo 44.1kHz -> must become 15s mono at 22.05kHz.
	r := raw{samples: make([]int16, 44100*2*20), rate: 44100, channels: 2}
	for i := range r.samples {
		r.samples[i] = 1000
	}
	p, _ := normalise(&r, 15)
	if want := int((15 + LeadIn) * SampleRate); len(p) != want {
		t.Fatalf("len=%d want %d", len(p), want)
	}
	if lead := p[:int(LeadIn*SampleRate)]; lead[0] != 0 || lead[len(lead)-1] != 0 {
		t.Fatal("lead-in must be silent")
	}
	if v := p[int(LeadIn*SampleRate)+2000]; v < 29000 {
		t.Fatalf("loudness not normalised to ~-1dBFS: %d", v)
	}
	if p[len(p)-1] != 0 {
		t.Fatalf("fade-out should end at silence, got %d", p[len(p)-1])
	}
}

func TestWAVRoundTrip(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.wav")
	in := PCM{0, 1, -1, 32767, -32768}
	if err := WriteWAV(f, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadPCM(f)
	if err != nil || len(out) != len(in) || out[3] != 32767 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestConvertTrimsLongWAV(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "long.wav"), filepath.Join(dir, "out.wav")
	if err := WriteWAV(src, make(PCM, 20*SampleRate)); err != nil {
		t.Fatal(err)
	}
	c, err := Convert(src, dst, 15)
	if err != nil {
		t.Fatal(err)
	}
	if c.Len != 15 || c.Full != 20 {
		t.Fatalf("clip=%v full=%v want 15/20", c.Len, c.Full)
	}
}

func TestBuiltinsDecode(t *testing.T) {
	b := Builtins()
	if len(b) == 0 || DefaultID() != b[0].ID {
		t.Fatal("no builtins embedded")
	}
	dir := t.TempDir()
	for _, s := range b {
		f := filepath.Join(dir, s.ID+".wav")
		if err := WriteBuiltin(s.ID, f, 15); err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		p, _ := ReadPCM(f)
		if pk := Peaks(p, 20); pk[10] == 0 && pk[15] == 0 && pk[19] == 0 && pk[5] == 0 {
			t.Errorf("%s looks silent", s.ID)
		}
	}
}

// A quiet intro then a loud repeated riff: the hook finder must skip the intro.
func TestBestStartSkipsQuietIntro(t *testing.T) {
	rate := SampleRate
	p := make([]float32, 40*rate)
	for i := range p {
		sec := float64(i) / float64(rate)
		amp := 300.0 // intro
		if sec >= 20 {
			amp = 12000 // chorus
		}
		// chorus: beat every 0.5s on a 220Hz tone
		beat := 1.0
		if sec >= 20 {
			beat = math.Exp(-8 * math.Mod(sec, 0.5))
		}
		p[i] = float32(amp * beat * math.Sin(2*math.Pi*220*sec))
	}
	start := float64(BestStart(p, rate, 15)) / float64(rate)
	if start < 19 || start > 25.1 {
		t.Fatalf("picked %.1fs, want inside the loud part (19..25)", start)
	}
}

func TestFFTImpulse(t *testing.T) {
	a := make([]complex128, 8)
	a[0] = 1
	fft(a, twiddles(8))
	for _, v := range a {
		if math.Abs(real(v)-1) > 1e-9 || math.Abs(imag(v)) > 1e-9 {
			t.Fatal(a)
		}
	}
}
