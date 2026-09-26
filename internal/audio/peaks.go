package audio

// Peaks buckets p into n absolute-peak values in [0,1] for waveform drawing.
func Peaks(p PCM, n int) []float64 {
	out := make([]float64, n)
	if len(p) == 0 || n == 0 {
		return out
	}
	top := 1.0
	for i := range n {
		a, b := i*len(p)/n, (i+1)*len(p)/n
		m := 0
		for _, s := range p[a:max(b, a+1)] {
			if s < 0 {
				s = -s
			}
			m = max(m, int(s))
		}
		out[i] = float64(m)
		top = max(top, out[i])
	}
	for i := range out {
		out[i] /= top
	}
	return out
}
