package audio

import "math"

// Hook finder: when a track is longer than the clip, pick the part people
// actually remember instead of the intro.
//
// Per ~46ms frame we measure loudness (RMS) and onset strength (spectral
// flux: how much new energy appears, i.e. drums/syllables). Per 1s block we
// build a 12-bin chroma vector (energy per pitch class). A candidate window
// then scores:
//
//	loudness  – choruses are louder than verses/intros
//	punch     – onset density: something is happening
//	repeat    – best chroma match with another part of the track: the
//	            section that comes back is the hook/chorus
//	quiet pen – windows that start or contain near-silence
//
// The winner's start is snapped back to the strongest onset within 0.6s so
// the clip begins on a hit, not mid-note.

const (
	fftN   = 512  // 21Hz bins at 11kHz: enough for chroma via harmonics
	hop    = 1024 // ~93ms frames: plenty for picking a window
	blockS = 1.0
)

// BestStart returns the sample offset of the best sec-long window in p.
func BestStart(p []float32, rate int, sec float64) int {
	win := int(sec * float64(rate))
	if len(p) <= win || rate <= 0 {
		return 0
	}
	// Analyse at half rate: 11kHz still covers every note's fundamental and
	// the attack band, and halves the FFT work.
	d := decimate(p)
	return min(bestStart(d, rate/2, sec)*2, len(p)-win)
}

func decimate(p []float32) []float32 {
	d := make([]float32, len(p)/2)
	for i := range d {
		d[i] = (p[2*i] + p[2*i+1]) * 0.5
	}
	return d
}

func bestStart(p []float32, rate int, sec float64) int {
	win := int(sec * float64(rate))
	frames := (len(p) - hop) / hop
	if frames < 4 {
		return 0
	}
	rms := make([]float64, frames)
	flux := make([]float64, frames)
	fps := float64(rate) / hop
	perBlock := max(int(blockS*fps), 1)
	nBlocks := frames/perBlock + 1
	chroma := make([][12]float64, nBlocks)

	hann := make([]float64, fftN)
	for i := range hann {
		hann[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/fftN)
	}
	// bin -> pitch class, only 60Hz..5kHz (musical range)
	pc := make([]int, fftN/2)
	for k := range pc {
		f := float64(k) * float64(rate) / fftN
		if f < 60 || f > 5000 {
			pc[k] = -1
			continue
		}
		pc[k] = (int(math.Round(12*math.Log2(f/440)))%12 + 12 + 9) % 12
	}

	buf := make([]complex128, fftN)
	tw := twiddles(fftN)
	prev := make([]float64, fftN/2)
	for fr := range frames {
		off := fr * hop
		e := 0.0
		for i := range hop {
			v := float64(p[off+i])
			e += v * v
			if i < fftN {
				buf[i] = complex(v*hann[i], 0)
			}
		}
		rms[fr] = math.Sqrt(e / hop)
		fft(buf, tw)
		b := fr / perBlock
		for k := 1; k < fftN/2; k++ {
			re, im := real(buf[k]), imag(buf[k])
			pw := re*re + im*im
			mag := math.Sqrt(pw)
			if d := mag - prev[k]; d > 0 {
				flux[fr] += d
			}
			prev[k] = mag
			if pc[k] >= 0 {
				chroma[b][pc[k]] += pw
			}
		}
	}
	for b := range chroma {
		n := 0.0
		for _, v := range chroma[b] {
			n += v * v
		}
		if n = math.Sqrt(n); n > 0 {
			for i := range chroma[b] {
				chroma[b][i] /= n
			}
		}
	}

	diag := blockDiag(chroma)
	loud := dbNorm(rms)
	punch := zNorm(flux)
	winF := int(sec * fps)
	winB := max(int(sec/blockS), 1)
	step := max(int(0.5*fps), 1)

	best, bestScore := 0, math.Inf(-1)
	for s := 0; s+winF <= frames; s += step {
		l, pu, quiet := 0.0, 0.0, 0
		for f := s; f < s+winF; f++ {
			l += loud[f]
			pu += punch[f]
			if loud[f] < 0.15 {
				quiet++
			}
		}
		l /= float64(winF)
		pu /= float64(winF)
		rep := repetition(diag, s/perBlock, winB)
		score := 1.0*l + 0.35*pu + 0.8*rep - 1.5*float64(quiet)/float64(winF)
		if s < int(2*fps) { // tiny nudge away from cold opens
			score -= 0.05
		}
		if score > bestScore {
			best, bestScore = s, score
		}
	}

	// snap back to the strongest onset just before the window
	snap, peak := best, -1.0
	for f := max(best-int(0.6*fps), 0); f <= best && f < frames; f++ {
		if flux[f] > peak {
			snap, peak = f, flux[f]
		}
	}
	return min(snap*hop, len(p)-win)
}

// blockDiag returns D where D[i][j] = sum of cos-sim(block i-t, block j-t)
// for t ≥ 0 along the diagonal: any run's similarity is then O(1).
func blockDiag(ch [][12]float64) [][]float64 {
	n := len(ch)
	d := make([][]float64, n)
	for i := range d {
		d[i] = make([]float64, n)
		for j := range n {
			sim := 0.0
			for k := range 12 {
				sim += ch[i][k] * ch[j][k]
			}
			if i > 0 && j > 0 {
				sim += d[i-1][j-1]
			}
			d[i][j] = sim
		}
	}
	return d
}

// repetition: best mean cosine similarity between the window's n blocks
// and a same-length run elsewhere (at least half a window away).
func repetition(d [][]float64, start, n int) float64 {
	if start+n > len(d) || n == 0 {
		return 0
	}
	end := start + n - 1
	best := 0.0
	for j := 0; j+n <= len(d); j++ {
		if abs(j-start) < n/2+1 {
			continue
		}
		sum := d[end][j+n-1]
		if start > 0 && j > 0 {
			sum -= d[start-1][j-1]
		}
		best = math.Max(best, sum/float64(n))
	}
	return best
}

// dbNorm maps RMS to 0..1 on a 50dB range below the track's peak.
func dbNorm(x []float64) []float64 {
	top := 1e-9
	for _, v := range x {
		top = math.Max(top, v)
	}
	out := make([]float64, len(x))
	for i, v := range x {
		db := 20 * math.Log10(math.Max(v, 1e-9)/top)
		out[i] = math.Max(0, 1+db/50)
	}
	return out
}

func zNorm(x []float64) []float64 {
	mean, sd := 0.0, 0.0
	for _, v := range x {
		mean += v
	}
	mean /= float64(len(x))
	for _, v := range x {
		sd += (v - mean) * (v - mean)
	}
	sd = math.Sqrt(sd/float64(len(x))) + 1e-9
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = (v - mean) / sd
	}
	return out
}

// twiddles precomputes e^(-2πik/n) for k < n/2.
func twiddles(n int) []complex128 {
	t := make([]complex128, n/2)
	for k := range t {
		a := -2 * math.Pi * float64(k) / float64(n)
		t[k] = complex(math.Cos(a), math.Sin(a))
	}
	return t
}

// fft: in-place iterative radix-2 Cooley–Tukey with a precomputed twiddle
// table (tw = twiddles(len(a))). len(a) must be a power of 2.
func fft(a []complex128, tw []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, stride := size/2, n/size
		for start := 0; start < n; start += size {
			for k := range half {
				u, v := a[start+k], a[start+k+half]*tw[k*stride]
				a[start+k], a[start+k+half] = u+v, u-v
			}
		}
	}
}
