package tui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// Palette slots. Users override any slot via config "colors".
type Palette struct {
	Fg, Dim, Accent, Accent2, Hot, Ok, Border string
}

// "terminal" uses the 16 ANSI colors, so faaa inherits whatever theme the
// terminal already runs (Omarchy, rose-pine, your own). Everything else is
// a fixed palette for people who want faaa to look the same everywhere.
var Themes = map[string]Palette{
	"terminal":   {"", "8", "3", "3", "1", "2", "8"},
	"ink":        {"#E8E8E8", "#6B6B6B", "#F05033", "#F05033", "#F05033", "#E8E8E8", "#2A2A2A"}, // mono + git orange
	"tokyonight": {"#C0CAF5", "#565F89", "#7AA2F7", "#7AA2F7", "#FF9E64", "#9ECE6A", "#292E42"},
	"catppuccin": {"#CDD6F4", "#6C7086", "#89B4FA", "#89B4FA", "#FAB387", "#A6E3A1", "#313244"},
	"rosepine":   {"#E0DEF4", "#6E6A86", "#EBBCBA", "#EBBCBA", "#F6C177", "#9CCFD8", "#26233A"},
	"gruvbox":    {"#EBDBB2", "#7C6F64", "#FE8019", "#FE8019", "#FB4934", "#B8BB26", "#3C3836"},
	"nord":       {"#ECEFF4", "#4C566A", "#88C0D0", "#88C0D0", "#EBCB8B", "#A3BE8C", "#3B4252"},
}

// DefaultTheme follows the terminal.
const DefaultTheme = "terminal"

func ThemeNames() []string {
	var n []string
	for k := range Themes {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func resolve(name string, over map[string]string) Palette {
	p, ok := Themes[name]
	if !ok {
		p = Themes[DefaultTheme]
	}
	for k, v := range over {
		switch k {
		case "fg":
			p.Fg = v
		case "dim":
			p.Dim = v
		case "accent":
			p.Accent = v
		case "accent2":
			p.Accent2 = v
		case "hot":
			p.Hot = v
		case "ok":
			p.Ok = v
		case "border":
			p.Border = v
		}
	}
	return p
}

type styles struct {
	p                                      Palette
	fg, dim, accent, accent2, hot, ok, sel lipgloss.Style
	box, boxFocus                          lipgloss.Style
	grad                                   []lipgloss.Style // accent -> accent2
	gradDim                                []lipgloss.Style
}

const gradSteps = 24

func newStyles(p Palette) styles {
	c := func(h string) lipgloss.Color { return lipgloss.Color(h) }
	s := styles{p: p}
	s.fg = lipgloss.NewStyle()
	if p.Fg != "" {
		s.fg = s.fg.Foreground(c(p.Fg)) // "" = terminal's own foreground
	}
	s.dim = lipgloss.NewStyle().Foreground(c(p.Dim))
	s.accent = lipgloss.NewStyle().Foreground(c(p.Accent)).Bold(true)
	s.accent2 = lipgloss.NewStyle().Foreground(c(p.Accent2)).Bold(true)
	s.hot = lipgloss.NewStyle().Foreground(c(p.Hot)).Bold(true)
	s.ok = lipgloss.NewStyle().Foreground(c(p.Ok)).Bold(true)
	s.sel = lipgloss.NewStyle().Foreground(c(p.Accent)).Bold(true)
	s.box = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c(p.Border)).Padding(0, 1)
	s.boxFocus = s.box.BorderForeground(c(p.Accent))
	ansi := len(p.Accent) == 0 || p.Accent[0] != '#'
	for i := range gradSteps {
		if ansi { // no blending in ANSI space: played = accent, rest = dim
			s.grad = append(s.grad, lipgloss.NewStyle().Foreground(c(p.Accent)))
			s.gradDim = append(s.gradDim, lipgloss.NewStyle().Foreground(c(p.Dim)))
			continue
		}
		t := float64(i) / (gradSteps - 1)
		col := mix(p.Accent, p.Accent2, t)
		s.grad = append(s.grad, lipgloss.NewStyle().Foreground(c(col)))
		s.gradDim = append(s.gradDim, lipgloss.NewStyle().Foreground(c(mix(col, p.Border, 0.65))))
	}
	return s
}

func hex(h string) (r, g, b int) {
	if len(h) == 7 && h[0] == '#' {
		var v [3]int
		for i := range 3 {
			for _, ch := range h[1+i*2 : 3+i*2] {
				v[i] *= 16
				switch {
				case ch >= '0' && ch <= '9':
					v[i] += int(ch - '0')
				case ch >= 'a' && ch <= 'f':
					v[i] += int(ch-'a') + 10
				case ch >= 'A' && ch <= 'F':
					v[i] += int(ch-'A') + 10
				}
			}
		}
		return v[0], v[1], v[2]
	}
	return 200, 200, 200
}

func mix(a, b string, t float64) string {
	r1, g1, b1 := hex(a)
	r2, g2, b2 := hex(b)
	l := func(x, y int) int { return x + int(float64(y-x)*t) }
	const d = "0123456789ABCDEF"
	out := []byte("#000000")
	for i, v := range []int{l(r1, r2), l(g1, g2), l(b1, b2)} {
		out[1+i*2], out[2+i*2] = d[v>>4], d[v&15]
	}
	return string(out)
}

// gradient colours text char-by-char across the accent gradient.
func (s styles) gradient(text string, offset int) string {
	r := []rune(text)
	out := ""
	for i, ch := range r {
		k := (i*gradSteps/max(len(r), 1) + offset) % (2 * gradSteps)
		if k >= gradSteps {
			k = 2*gradSteps - 1 - k
		}
		out += s.grad[k].Render(string(ch))
	}
	return out
}
