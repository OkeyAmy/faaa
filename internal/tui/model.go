// Package tui is the interactive sound picker.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/okeyamy/faaa/internal/audio"
	"github.com/okeyamy/faaa/internal/catalog"
	"github.com/okeyamy/faaa/internal/config"
	"github.com/okeyamy/faaa/internal/fetch"
	"github.com/okeyamy/faaa/internal/hook"
	"github.com/okeyamy/faaa/internal/paths"
	"github.com/okeyamy/faaa/internal/search"
	"github.com/okeyamy/faaa/internal/tools"
)

var filters = []string{"all", "meme", "classic", "mine"}
var filterLabel = map[string]string{"all": "all", "meme": "trending", "classic": "classics", "mine": "mine"}

type (
	tickMsg  time.Time
	indexMsg struct{ ix catalog.Index }
	readyMsg struct {
		id   string
		play bool
		err  error
		pre  bool // background prefetch: don't arm or play
	}
	hoverMsg   struct{ id string }
	playEndMsg struct{ id string }
	warmMsg    struct{}
	addedMsg   struct {
		s   catalog.Sound
		err error
	}
	searchMsg struct{ q string } // debounce fired
	webMsg    struct {
		q   string
		res []catalog.Sound
		err error
	}
)

type Model struct {
	cfg      config.Config
	st       styles
	all      []catalog.Sound
	view     []catalog.Sound
	cur, top int
	filter   int
	search   textinput.Model
	searchOn bool
	w, h     int

	peaks    map[string][]float64
	loading  map[string]bool
	playing  string
	player   *exec.Cmd
	started  time.Time
	dur      float64
	frame    int
	status   string
	statusAt time.Time
	hooked   bool
	ticking  bool
	animAt   time.Time       // selection changed: hero waveform grows in
	web      []catalog.Sound // live search results for webQ
	webQ     string
	webBusy  bool
	pre      map[string]bool // prefetches in flight
}

func prefetch(s catalog.Sound, maxSec float64) tea.Cmd {
	return func() tea.Msg { return readyMsg{id: s.ID, err: fetch.Ensure(s, maxSec), pre: true} }
}

// hover schedules a prefetch if the cursor rests on a sound for 120ms.
func (m *Model) hover() tea.Cmd {
	s, ok := m.selected()
	if !ok || s.Source == "link" || catalog.Ready(s.ID) || m.pre[s.ID] {
		return nil
	}
	id := s.ID
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return hoverMsg{id} })
}

const growDur = 280 * time.Millisecond

// startTick runs the 30fps animation clock only while something moves, so
// the idle TUI costs 0% CPU.
func (m *Model) startTick() tea.Cmd {
	if m.ticking {
		return nil
	}
	m.ticking = true
	return tick()
}

func (m *Model) animating() bool {
	return m.playing != "" || len(m.loading) > 0 || m.webBusy || time.Since(m.animAt) < growDur || time.Since(m.statusAt) < 600*time.Millisecond
}

func New(cfg config.Config, flash string) Model {
	ti := textinput.New()
	ti.Placeholder = "search sounds, artists, tags…"
	ti.Prompt = "  "
	m := Model{
		cfg: cfg, st: newStyles(resolve(cfg.Theme, cfg.Colors)),
		search: ti, peaks: map[string][]float64{}, loading: map[string]bool{}, pre: map[string]bool{},
		hooked: hook.Installed(),
	}
	m.all = catalog.All(catalog.Cached())
	if _, ok := catalog.Find(cfg.Sound); !ok { // stale id from an older version
		m.cfg.Sound = audio.DefaultID()
		_ = config.Save(m.cfg)
	}
	m.apply()
	if flash != "" {
		m.flash(flash)
	}
	m.animAt = time.Now()
	for i, s := range m.view {
		if s.ID == m.cfg.Sound {
			m.cur = i
		}
	}
	return m
}

func (m Model) Init() tea.Cmd {
	url := m.cfg.CatalogURL
	maxSec := m.cfg.MaxSeconds
	warm := func() tea.Msg { // decode builtins once, in the background
		for _, b := range audio.Builtins() {
			if !catalog.Ready(b.ID) {
				_ = audio.WriteBuiltin(b.ID, paths.Sound(b.ID), maxSec)
			}
		}
		return warmMsg{}
	}
	var first []tea.Cmd // prefetch the top trending sounds (~50KB each)
	for _, s := range m.all {
		if len(first) == 6 {
			break
		}
		if s.Source == "meme" && !catalog.Ready(s.ID) {
			first = append(first, prefetch(s, maxSec))
			m.pre[s.ID] = true
		}
	}
	go fetch.Warm()
	return tea.Batch(append(first, tick(), warm, func() tea.Msg {
		ix, changed, _ := catalog.Refresh(url)
		if !changed {
			return nil
		}
		return indexMsg{ix}
	})...)
}

// apply recomputes the visible list from filter + fuzzy search.
func (m *Model) apply() {
	if raw := strings.TrimSpace(m.search.Value()); tools.IsLink(raw) {
		m.view = []catalog.Sound{{ID: "\x00link", Title: "grab the sound from this link", Source: "link", Page: raw}}
		m.cur = 0
		return
	}
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))
	f := filters[m.filter]
	type scored struct {
		s catalog.Sound
		n int
	}
	var out []scored
	for _, s := range m.all {
		if f != "all" && s.Source != f {
			continue
		}
		n := 0
		if q != "" {
			n = fuzzy(q, strings.ToLower(s.Title+" "+s.Artist+" "+strings.Join(s.Tags, " ")+" "+s.ID))
			if n < 0 {
				continue
			}
		}
		out = append(out, scored{s, n})
	}
	if q != "" { // stable insertion sort by score: lists are small
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j].n > out[j-1].n; j-- {
				out[j], out[j-1] = out[j-1], out[j]
			}
		}
	}
	m.view = m.view[:0]
	seen := map[string]bool{}
	for _, o := range out {
		m.view = append(m.view, o.s)
		seen[o.s.ID] = true
	}
	if q != "" && strings.EqualFold(strings.TrimSpace(m.webQ), q) {
		for _, s := range m.web { // live results after local matches
			if !seen[s.ID] && (f == "all" || f == s.Source) {
				seen[s.ID] = true
				m.view = append(m.view, s)
			}
		}
	}
	m.cur = min(m.cur, max(len(m.view)-1, 0))
}

// fuzzy: subsequence match; score rewards consecutive + word-start hits.
func fuzzy(q, s string) int {
	score, qi, prev := 0, 0, -2
	qr, sr := []rune(q), []rune(s)
	for i, r := range sr {
		if qi < len(qr) && r == qr[qi] {
			score++
			if prev == i-1 {
				score += 3
			}
			if i == 0 || !unicode.IsLetter(sr[i-1]) {
				score += 2
			}
			prev = i
			qi++
		}
	}
	if qi < len(qr) {
		return -1
	}
	return score
}

func inCatalog(all []catalog.Sound, id string) bool {
	for _, s := range all {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (m Model) selected() (catalog.Sound, bool) {
	if len(m.view) == 0 {
		return catalog.Sound{}, false
	}
	return m.view[m.cur], true
}

func (m *Model) flash(s string) { m.status, m.statusAt = s, time.Now() }

func tick() tea.Cmd {
	return tea.Tick(time.Second/30, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) stop() {
	if m.player != nil && m.player.Process != nil {
		_ = m.player.Process.Kill()
	}
	m.player, m.playing = nil, ""
}

func (m *Model) loadPeaks(id string) {
	if _, ok := m.peaks[id]; ok {
		return
	}
	if p, err := audio.ReadPCM(paths.Sound(id)); err == nil {
		m.peaks[id] = audio.Peaks(p, 400)
		m.peaks[id+"#dur"] = []float64{float64(len(p)) / audio.SampleRate}
	}
}

func ensure(s catalog.Sound, maxSec float64, play bool) tea.Cmd {
	return func() tea.Msg { return readyMsg{id: s.ID, play: play, err: fetch.Ensure(s, maxSec)} }
}

func (m *Model) startPlay(id string) tea.Cmd {
	m.stop()
	c, err := audio.Command(m.cfg.Player, paths.Sound(id))
	if err != nil {
		m.flash("✗ " + err.Error())
		return nil
	}
	if err := c.Start(); err != nil {
		m.flash("✗ " + err.Error())
		return nil
	}
	m.loadPeaks(id)
	m.player, m.playing, m.started = c, id, time.Now()
	m.dur = 1
	if d, ok := m.peaks[id+"#dur"]; ok {
		m.dur = d[0]
	}
	return tea.Batch(m.startTick(), func() tea.Msg { _ = c.Wait(); return playEndMsg{id} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case indexMsg:
		m.all = catalog.All(msg.ix)
		m.apply()
		m.flash(fmt.Sprintf("↻ %d fresh sounds", len(msg.ix.Sounds)))
	case tickMsg:
		m.frame++
		if m.animating() {
			return m, tick()
		}
		m.ticking = false
	case searchMsg:
		if msg.q != m.search.Value() || len(strings.TrimSpace(msg.q)) < 2 || msg.q == m.webQ || tools.IsLink(msg.q) {
			return m, nil // user kept typing, or already searched
		}
		m.webBusy = true
		q := msg.q
		return m, tea.Batch(m.startTick(), func() tea.Msg {
			res, err := search.Web(q)
			return webMsg{q, res, err}
		})
	case webMsg:
		if msg.q != m.search.Value() {
			return m, nil
		}
		m.webBusy = false
		m.web, m.webQ = msg.res, msg.q
		if msg.err != nil {
			m.flash("○ offline — showing local matches")
		}
		m.apply()
		return m, nil
	case addedMsg:
		delete(m.loading, "\x00link")
		if msg.err != nil {
			m.flash("✗ " + msg.err.Error())
			return m, nil
		}
		m.cfg = config.Load()
		m.cfg.Sound = msg.s.ID
		_ = config.Save(m.cfg)
		m.all = catalog.All(catalog.Cached())
		m.search.SetValue("")
		m.filter = 3 // mine
		m.apply()
		for i, s := range m.view {
			if s.ID == msg.s.ID {
				m.cur = i
			}
		}
		m.flash("◉ armed — next git push plays " + msg.s.Title)
		return m, tea.Batch(m.startTick(), m.startPlay(msg.s.ID))
	case warmMsg:
		m.animAt = time.Now() // re-grow hero with real peaks
		return m, m.startTick()
	case playEndMsg:
		if m.playing == msg.id {
			m.player, m.playing = nil, ""
		}
	case hoverMsg:
		s, ok := m.selected()
		if !ok || s.ID != msg.id || catalog.Ready(s.ID) || m.pre[s.ID] || m.loading[s.ID] {
			return m, nil
		}
		m.pre[s.ID] = true
		return m, prefetch(s, m.cfg.MaxSeconds)
	case readyMsg:
		if msg.pre {
			delete(m.pre, msg.id)
			if msg.err == nil {
				m.loadPeaks(msg.id)
				if s, ok := m.selected(); ok && s.ID == msg.id {
					m.animAt = time.Now() // real waveform grows in
					return m, m.startTick()
				}
			}
			return m, nil
		}
		delete(m.loading, msg.id)
		if msg.err != nil {
			m.flash("✗ " + msg.err.Error())
			return m, nil
		}
		m.loadPeaks(msg.id)
		if msg.play {
			return m, m.startPlay(msg.id)
		}
		m.cfg.Sound = msg.id
		_ = config.Save(m.cfg)
		title := msg.id
		if s, ok := m.selected(); ok && s.ID == msg.id {
			title = s.Title
			if !inCatalog(m.all, s.ID) { // from live search: keep it
				s.Source = "mine"
				_ = catalog.AddMine(s)
				m.all = catalog.All(catalog.Cached())
			}
		}
		m.flash("◉ armed — next git push plays " + title)
		return m, tea.Batch(m.startTick(), m.startPlay(msg.id))
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchOn {
		switch k.String() {
		case "esc":
			m.searchOn = false
			m.search.Blur()
			m.search.SetValue("")
			m.apply()
			return m, nil
		case "enter", "down", "up":
			m.searchOn = false
			m.search.Blur()
			if k.String() == "enter" {
				if s, ok := m.selected(); ok && s.Source == "link" {
					return m.handle(k, s, ok)
				}
				return m, nil
			}
		default:
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(k)
			m.cur, m.top = 0, 0
			m.apply()
			q := m.search.Value()
			debounce := tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return searchMsg{q} })
			return m, tea.Batch(cmd, debounce)
		}
	}
	cur, filter := m.cur, m.filter
	s, ok := m.selected()
	nm, cmd := m.handle(k, s, ok)
	if mm, isModel := nm.(Model); isModel && (mm.cur != cur || mm.filter != filter) {
		mm.animAt = time.Now() // new selection: grow its waveform in
		return mm, tea.Batch(cmd, mm.startTick(), mm.hover())
	}
	return nm, cmd
}

func (m Model) handle(k tea.KeyMsg, s catalog.Sound, ok bool) (tea.Model, tea.Cmd) {
	if ok && s.Source == "link" && (k.String() == "enter" || k.String() == " ") {
		if m.loading[s.ID] {
			return m, nil
		}
		m.loading[s.ID] = true
		link, maxSec := s.Page, m.cfg.MaxSeconds
		m.flash("⇣ grabbing the sound… (first time also sets up the downloader)")
		return m, tea.Batch(m.startTick(), func() tea.Msg {
			snd, _, err := fetch.Add(link, "", maxSec, nil)
			return addedMsg{snd, err}
		})
	}
	switch k.String() {
	case "ctrl+c", "q":
		m.stop()
		return m, tea.Quit
	case "esc":
		if m.playing != "" {
			m.stop()
		} else if m.search.Value() != "" {
			m.search.SetValue("")
			m.apply()
		} else {
			return m, tea.Quit
		}
	case "/":
		m.searchOn = true
		return m, m.search.Focus()
	case "up", "k":
		m.cur = max(m.cur-1, 0)
	case "down", "j":
		m.cur = min(m.cur+1, max(len(m.view)-1, 0))
	case "pgup":
		m.cur = max(m.cur-10, 0)
	case "pgdown":
		m.cur = min(m.cur+10, max(len(m.view)-1, 0))
	case "g", "home":
		m.cur = 0
	case "G", "end":
		m.cur = max(len(m.view)-1, 0)
	case "tab", "right", "l":
		m.filter = (m.filter + 1) % len(filters)
		m.cur, m.top = 0, 0
		m.apply()
	case "shift+tab", "left", "h":
		m.filter = (m.filter + len(filters) - 1) % len(filters)
		m.cur, m.top = 0, 0
		m.apply()
	case "t":
		names := ThemeNames()
		i := 0
		for j, n := range names {
			if n == m.cfg.Theme {
				i = j
			}
		}
		m.cfg.Theme = names[(i+1)%len(names)]
		m.st = newStyles(resolve(m.cfg.Theme, m.cfg.Colors))
		_ = config.Save(m.cfg)
		m.flash("◐ " + m.cfg.Theme)
		return m, m.startTick()
	case " ", "p":
		if !ok {
			break
		}
		if m.playing == s.ID {
			m.stop()
			break
		}
		if catalog.Ready(s.ID) {
			return m, m.startPlay(s.ID)
		}
		m.loading[s.ID] = true
		return m, tea.Batch(ensure(s, m.cfg.MaxSeconds, true), m.startTick())
	case "enter":
		if !ok {
			break
		}
		m.loading[s.ID] = true
		return m, tea.Batch(ensure(s, m.cfg.MaxSeconds, false), m.startTick())
	case "[", "]":
		d := 5.0
		if k.String() == "[" {
			d = -5
		}
		v := min(max(m.cfg.MaxSeconds+d, 5), 60)
		if v == m.cfg.MaxSeconds {
			break
		}
		m.cfg.MaxSeconds = v
		_ = config.Save(m.cfg)
		m.flash(fmt.Sprintf("✂ clips up to %gs — best part picked", v))
		// drop cached cuts; re-cut the armed one now so pushes never go silent
		m.peaks = map[string][]float64{}
		if e, err := os.ReadDir(paths.Sounds()); err == nil {
			for _, f := range e {
				if f.Name() != m.cfg.Sound+".wav" {
					_ = os.Remove(filepath.Join(paths.Sounds(), f.Name()))
				}
			}
		}
		armed, ok := catalog.Find(m.cfg.Sound)
		if !ok {
			return m, m.startTick()
		}
		m.loading[armed.ID] = true
		recut := func() tea.Msg {
			err := fetch.Recut(armed, v)
			return readyMsg{id: armed.ID, err: err}
		}
		return m, tea.Batch(m.startTick(), recut)
	case "x":
		m.cfg.Enabled = !m.cfg.Enabled
		_ = config.Save(m.cfg)
		m.flash(map[bool]string{true: "🔊 push sounds on", false: "🔇 push sounds muted"}[m.cfg.Enabled])
	}
	return m, nil
}

// Run starts the TUI in the alt screen. flash is an initial status line.
func Run(cfg config.Config, flash string) error {
	_, err := tea.NewProgram(New(cfg, flash), tea.WithAltScreen(), tea.WithOutput(os.Stderr)).Run()
	return err
}
