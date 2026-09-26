package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/okeyamy/faaa/internal/audio"
	"github.com/okeyamy/faaa/internal/catalog"
	"github.com/okeyamy/faaa/internal/paths"
)

var spin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func clock(sec float64) string {
	s := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// spread places left and right on one line of width w.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", max(gap, 1)) + right
}

// fileDuration derives clip length from the normalised WAV's size (a stat,
// no read). 0 if not downloaded yet.
func fileDuration(id string) float64 {
	fi, err := os.Stat(paths.Sound(id))
	if err != nil {
		return 0
	}
	return max(float64(fi.Size()-44)/2/audio.SampleRate-audio.LeadIn, 0)
}

// easeOut for the waveform grow-in.
func easeOut(t float64) float64 { return 1 - math.Pow(1-min(max(t, 0), 1), 3) }

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	st := m.st
	pad := 2
	if m.w < 60 {
		pad = 1
	}
	inner := m.w - 2*pad
	gut := strings.Repeat(" ", pad)
	var out []string
	add := func(l string) { out = append(out, gut+l) }

	// ── header: logo + armed state
	shimmer := 0
	if m.ticking {
		shimmer = m.frame / 2
	}
	logo := st.gradient("f a a a", shimmer) + st.dim.Render("   git push, but louder")
	if inner < 50 {
		logo = st.gradient("f a a a", shimmer)
	}
	armed := st.dim.Render(fmt.Sprintf("✂ %gs   ", m.cfg.MaxSeconds)) + st.ok.Render("◉ armed")
	switch {
	case !m.cfg.Enabled:
		armed = st.dim.Render("○ muted")
	case !m.hooked:
		armed = st.hot.Render("○ not armed")
	}
	out = append(out, "")
	add(spread(logo, armed, inner))
	out = append(out, "")

	// ── hero: selected sound's waveform
	s, ok := m.selected()
	heroH := min(max(m.h/4, 4), 10) &^ 1
	listH := m.h - heroH - 11
	if !ok {
		for range heroH {
			out = append(out, "")
		}
		add("")
	} else {
		if _, have := m.peaks[s.ID]; !have && catalog.Ready(s.ID) {
			m.loadPeaks(s.ID) // map is shared: cached across frames
		}
		peaks, real := m.peaks[s.ID]
		if !real {
			peaks = fakePeaks(s.ID, 200)
		}
		grow := easeOut(float64(time.Since(m.animAt)) / float64(growDur))
		if grow < 1 {
			g := make([]float64, len(peaks))
			for i, v := range peaks {
				// bars rise with a slight left-to-right stagger
				d := float64(i) / float64(len(peaks)) * 0.35
				g[i] = v * easeOut((grow-d)/(1-0.35))
			}
			peaks = g
		}
		progress, pulse, elapsed := -1.0, 0.0, 0.0
		if m.playing == s.ID {
			elapsed = time.Since(m.started).Seconds()
			progress = min(elapsed/m.dur, 1)
			pulse = 0.3 + 0.3*math.Sin(float64(m.frame)/2)
		} else if !real {
			progress = silhouette
		}
		for _, l := range strings.Split(st.waveform(peaks, inner, heroH, progress, pulse), "\n") {
			add(l)
		}

		// title + transport
		title := st.accent.Render(trunc(s.Title, inner/2))
		if s.Artist != "" {
			title += st.dim.Render("  " + trunc(s.Artist, inner/4))
		}
		total := s.Duration
		if total == 0 {
			total = fileDuration(s.ID)
		}
		if d, ok := m.peaks[s.ID+"#dur"]; ok {
			total = d[0]
		}
		var right string
		switch {
		case m.loading[s.ID]:
			right = st.hot.Render(spin[m.frame%len(spin)] + " fetching")
		case m.playing == s.ID:
			barW := min(24, max(inner/5, 6))
			fill := int(progress * float64(barW))
			right = st.dim.Render(clock(elapsed)+" ") + st.accent.Render(strings.Repeat("━", fill)) +
				st.hot.Render("●") + st.dim.Render(strings.Repeat("─", max(barW-fill-1, 0))+" "+clock(total))
		case s.Source == "link":
			right = st.dim.Render("enter ↵ grab it")
		case !real:
			right = st.dim.Render("↓ space to fetch + play")
		default:
			right = st.dim.Render("space ▶  " + clock(total))
		}
		add("")
		add(spread(title, right, inner))
	}
	out = append(out, "")

	// ── tabs / search
	var tabs []string
	for i, f := range filters {
		if i == m.filter {
			tabs = append(tabs, st.accent.Render(filterLabel[f]))
		} else {
			tabs = append(tabs, st.dim.Render(filterLabel[f]))
		}
	}
	tabLine := strings.Join(tabs, st.dim.Render("  ·  "))
	search := st.dim.Render("/ search or paste a link")
	if m.searchOn || m.search.Value() != "" {
		search = st.accent.Render("⌕") + m.search.View()
		if m.webBusy {
			search += st.hot.Render(" " + spin[m.frame%len(spin)] + " searching everywhere")
		}
	}
	add(spread(search, tabLine, inner))
	out = append(out, "")

	// ── list
	listH = max(listH, 3)
	rows := m.listView(inner, listH)
	for _, r := range rows {
		add(r)
	}
	for range listH - len(rows) {
		out = append(out, "")
	}

	// ── footer
	out = append(out, "")
	foot := st.dim.Render("space play   enter arm   / search anything   tab kind   [ ] length   t theme   q quit")
	if inner < 92 {
		foot = st.dim.Render("space play  enter arm  / find  tab  q")
	}
	if m.status != "" && time.Since(m.statusAt) < 4*time.Second {
		foot = st.accent2.Render(m.status)
	}
	add(foot)
	return strings.Join(out, "\n")
}

func (m Model) listView(w, h int) []string {
	st := m.st
	if len(m.view) == 0 {
		if m.webBusy {
			return []string{st.dim.Render("looking everywhere…")}
		}
		if m.search.Value() != "" {
			return []string{st.dim.Render("nothing found. try fewer words, or: faaa add ./sound.mp3")}
		}
		return []string{st.dim.Render("nothing here yet — faaa add ./sound.mp3")}
	}
	top := 0
	if m.cur >= h {
		top = m.cur - h + 1
	}
	var rows []string
	for i := top; i < min(len(m.view), top+h); i++ {
		s := m.view[i]
		mark := " "
		switch {
		case m.loading[s.ID]:
			mark = st.hot.Render(spin[m.frame%len(spin)])
		case m.playing == s.ID:
			mark = st.accent2.Render("▶")
		case s.ID == m.cfg.Sound:
			mark = st.ok.Render("◉")
		}
		dur := ""
		if s.Duration == 0 {
			s.Duration = fileDuration(s.ID)
		}
		if s.Duration > 0 {
			dur = clock(min(s.Duration, m.cfg.MaxSeconds))
		}
		hot := ""
		if s.TrendRank > 0 && s.TrendRank <= 3 {
			hot = " 🔥"
		}
		right := mark + " " + fmt.Sprintf("%5s", dur)
		avail := w - 3 - lipgloss.Width(right) - lipgloss.Width(hot)
		title := trunc(s.Title, avail)
		artist := ""
		if s.Artist != "" && avail-lipgloss.Width(title) > 6 {
			artist = "  " + trunc(s.Artist, avail-lipgloss.Width(title)-2)
		}
		var left string
		if i == m.cur {
			left = st.gradient("▌", m.frame) + " " + st.accent.Render(title) + st.dim.Render(artist) + hot
		} else {
			left = "  " + st.fg.Render(title) + st.dim.Render(artist) + hot
		}
		rows = append(rows, spread(left, st.dim.Render(right), w))
	}
	return rows
}
