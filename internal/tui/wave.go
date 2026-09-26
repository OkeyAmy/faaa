package tui

import (
	"hash/fnv"
	"math"
	"strings"
)

// silhouette progress renders an all-dim wave (sound not downloaded yet).
const silhouette = -2.0

var bars = []rune(" ▁▂▃▄▅▆▇█")

// waveform draws mirrored bars (top half grows up, bottom mirror grows down).
// progress in [0,1] splits bright played part from dim unplayed part;
// progress<0 means not playing (all bright). pulse adds live wobble.
func (s styles) waveform(peaks []float64, width, height int, progress, pulse float64) string {
	if width <= 0 || height < 2 {
		return ""
	}
	cols := resample(peaks, width)
	half := height / 2
	head := -1
	if progress == silhouette {
		head = -2 // everything dim, no playhead
	} else if progress >= 0 {
		head = int(progress * float64(width))
	}
	lines := make([]string, 0, height)
	row := func(level func(v float64, r int) rune) {
		for r := range half {
			var b strings.Builder
			run, runStyle := []rune{}, -1
			flush := func() {
				if len(run) == 0 {
					return
				}
				st := s.grad[runStyle%gradSteps]
				if runStyle >= gradSteps {
					st = s.gradDim[runStyle-gradSteps]
				}
				b.WriteString(st.Render(string(run)))
				run = run[:0]
			}
			for x, v := range cols {
				if pulse > 0 && head >= 0 && abs(x-head) < 6 {
					v = math.Min(1, v*(1+pulse*float64(6-abs(x-head))/6))
				}
				k := x * gradSteps / width
				if head == -2 || (head >= 0 && x > head) {
					k += gradSteps
				}
				if x == head {
					flush()
					b.WriteString(s.hot.Render("│"))
					continue
				}
				if k != runStyle {
					flush()
					runStyle = k
				}
				run = append(run, level(v, r))
			}
			flush()
			lines = append(lines, b.String())
		}
	}
	// top: row 0 is highest. fill = v*half cells counted from bottom.
	row(func(v float64, r int) rune { return cell(v*float64(half)-float64(half-1-r), false) })
	row(func(v float64, r int) rune { return cell(v*float64(half)*0.6-float64(r), true) })
	return strings.Join(lines, "\n")
}

// cell maps a fill amount in cell units to a block char.
func cell(f float64, flip bool) rune {
	if f >= 1 {
		return '█'
	}
	if f <= 0 {
		return ' '
	}
	if flip { // bottom mirror: top-anchored partials don't exist; use shade
		switch {
		case f > 0.66:
			return '▓'
		case f > 0.33:
			return '▒'
		}
		return '░'
	}
	return bars[int(f*8)]
}

func resample(p []float64, n int) []float64 {
	out := make([]float64, n)
	if len(p) == 0 {
		return out
	}
	for i := range n {
		a, b := i*len(p)/n, max((i+1)*len(p)/n, i*len(p)/n+1)
		m := 0.0
		for _, v := range p[a:min(b, len(p))] {
			m = math.Max(m, v)
		}
		out[i] = m
	}
	return out
}

// fakePeaks gives an un-downloaded sound a stable, id-seeded silhouette.
func fakePeaks(id string, n int) []float64 {
	h := fnv.New32a()
	h.Write([]byte(id))
	seed := float64(h.Sum32()%1000) / 100
	out := make([]float64, n)
	for i := range out {
		x := float64(i) / float64(n)
		v := 0.35 + 0.25*math.Sin(x*9+seed) + 0.2*math.Sin(x*23+seed*2) + 0.1*math.Sin(x*57+seed*3)
		out[i] = math.Max(0.05, math.Min(1, v*math.Sin(math.Pi*math.Min(1, x*1.3))))
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
