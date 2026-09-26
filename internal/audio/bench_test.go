package audio

import (
	"math"
	"testing"
)

func synth(sec int) []float32 {
	p := make([]float32, sec*SampleRate)
	for i := range p {
		t := float64(i) / SampleRate
		p[i] = float32(8000 * math.Sin(2*math.Pi*220*t) * math.Exp(-6*math.Mod(t, 0.5)))
	}
	return p
}

func BenchmarkBestStart30s(b *testing.B) {
	p := synth(30)
	for b.Loop() {
		BestStart(p, SampleRate, 15)
	}
}

func BenchmarkBestStart4min(b *testing.B) {
	p := synth(240)
	for b.Loop() {
		BestStart(p, SampleRate, 15)
	}
}
