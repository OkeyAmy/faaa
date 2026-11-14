// Command downloads appends today's release-download total to
// assets/downloads.json and redraws assets/downloads.svg.
//
// GitHub exposes only current, per-asset counters; there is no download
// history endpoint. The JSON file is that history, one point per run.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type point struct {
	Date      string `json:"date"`
	Downloads int    `json:"downloads"`
}

func main() {
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		fmt.Fprintln(os.Stderr, "downloads: set GITHUB_REPOSITORY=owner/repo")
		os.Exit(2)
	}
	histPath := filepath.Join("assets", "downloads.json")
	svgPath := filepath.Join("assets", "downloads.svg")

	var hist []point
	if b, err := os.ReadFile(histPath); err == nil {
		_ = json.Unmarshal(b, &hist)
	}
	n, err := binaries(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "downloads:", err)
		os.Exit(1)
	}
	today := time.Now().UTC().Format("2006-01-02")
	seen := false
	for i := range hist {
		if hist[i].Date == today {
			hist[i].Downloads, seen = n, true
		}
	}
	if !seen {
		hist = append(hist, point{Date: today, Downloads: n})
	}
	sort.Slice(hist, func(i, j int) bool { return hist[i].Date < hist[j].Date })

	b, _ := json.MarshalIndent(hist, "", "  ")
	if err := os.WriteFile(histPath, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "downloads:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(svgPath, []byte(chart(hist)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "downloads:", err)
		os.Exit(1)
	}
	fmt.Printf("%d downloads across %d points\n", n, len(hist))
}

// binaries sums release asset downloads, skipping checksums.txt: every
// installer fetches that too, so counting it would double every install.
func binaries(repo string) (int, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases?per_page=100", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "faaa-downloads")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("github answered %s", resp.Status)
	}
	var rels []struct {
		Assets []struct {
			Name      string `json:"name"`
			Downloads int    `json:"download_count"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return 0, err
	}
	sum := 0
	for _, r := range rels {
		for _, a := range r.Assets {
			if !strings.Contains(a.Name, "checksums") {
				sum += a.Downloads
			}
		}
	}
	return sum, nil
}

// chart draws a star-history-style line: one orange series, faint gridlines,
// dates along the bottom, latest total in the corner. Muted colours that read
// on both GitHub themes.
func chart(hist []point) string {
	const (
		w, h   = 820, 300
		left   = 48
		right  = 18
		top    = 34
		bottom = 34
		orange = "#F05033"
		grey   = "#8b949e"
	)
	x0, x1 := float64(left), float64(w-right)
	y0, y1 := float64(top), float64(h-bottom)

	if len(hist) == 0 {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="820" height="300"></svg>`
	}
	// One sample has no line to draw. Show the number as a card rather than a
	// lone dot on empty axes.
	if len(hist) == 1 {
		p := hist[0]
		d, _ := time.Parse("2006-01-02", p.Date)
		return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%d binary downloads since %s"><title>%d binary downloads since %s</title><g font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif"><text x="%d" y="146" text-anchor="middle" font-size="64" font-weight="600" fill="%s">%s</text><text x="%d" y="182" text-anchor="middle" font-size="13" fill="%s">binary downloads since %s</text></g></svg>`,
			w, h, w, h, p.Downloads, d.Format("Jan 2"), p.Downloads, d.Format("Jan 2"), w/2, orange, formatCount(p.Downloads), w/2, grey, d.Format("Jan 2"))
	}
	parse := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	first, last := parse(hist[0].Date), parse(hist[len(hist)-1].Date)
	span := last.Sub(first).Hours()
	if span <= 0 {
		span = 1
	}
	x := func(p point) float64 {
		if len(hist) == 1 {
			return (x0 + x1) / 2
		}
		return x0 + (x1-x0)*parse(p.Date).Sub(first).Hours()/span
	}
	maxY := math.Ceil(niceMax(float64(hist[len(hist)-1].Downloads))/5) * 5
	if maxY < 5 {
		maxY = 5
	}
	y := func(v int) float64 {
		return y1 - (y1-y0)*float64(v)/maxY
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="Release downloads over time">`, w, h, w, h)
	b.WriteString(`<title>Release downloads over time</title><defs><linearGradient id="fill" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="` + orange + `" stop-opacity="0.24"/><stop offset="1" stop-color="` + orange + `" stop-opacity="0"/></linearGradient></defs>`)
	b.WriteString(`<g font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif" font-size="11" fill="` + grey + `">`)

	// gridlines and y labels
	for i := 0; i <= 5; i++ {
		v := maxY * float64(i) / 5
		gy := y(int(v))
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="`+grey+`" stroke-opacity="0.16" stroke-width="1"/>`, x0, gy, x1, gy)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end">%s</text>`, x0-8, gy+4, formatCount(int(v)))
	}

	// area under the line (a single point has no area)
	if len(hist) > 1 {
		fmt.Fprintf(&b, `<path d="M%.1f,%.1f`, x(hist[0]), y1)
		for _, p := range hist {
			fmt.Fprintf(&b, ` L%.1f,%.1f`, x(p), y(p.Downloads))
		}
		fmt.Fprintf(&b, ` L%.1f,%.1f Z" fill="url(#fill)"/>`, x(hist[len(hist)-1]), y1)
	}

	// the line itself
	b.WriteString(`<polyline fill="none" stroke="` + orange + `" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" points="`)
	for i, p := range hist {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, `%.1f,%.1f`, x(p), y(p.Downloads))
	}
	b.WriteString(`"/>`)
	lastP := hist[len(hist)-1]
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="`+orange+`"/>`, x(lastP), y(lastP.Downloads))

	// latest total, top right; the section heading already names the series
	fmt.Fprintf(&b, `<text x="%.1f" y="18" text-anchor="end" font-weight="600">%s</text>`, x1, formatCount(lastP.Downloads))

	// date labels: one point gets one label, not three identical ones
	switch len(hist) {
	case 1:
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(hist[0]), float64(h-10), first.Format("Jan 2"))
	case 2:
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f">%s</text>`, x0, float64(h-10), first.Format("Jan 2"))
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end">%s</text>`, x1, float64(h-10), last.Format("Jan 2"))
	default:
		mid := hist[len(hist)/2]
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f">%s</text>`, x0, float64(h-10), first.Format("Jan 2"))
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(mid), float64(h-10), parse(mid.Date).Format("Jan 2"))
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end">%s</text>`, x1, float64(h-10), last.Format("Jan 2"))
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

func niceMax(v float64) float64 {
	if v <= 0 {
		return 4
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, step := range []float64{1, 2, 2.5, 5, 10} {
		if c := step * mag; c >= v {
			return c
		}
	}
	return 10 * mag
}

func formatCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
