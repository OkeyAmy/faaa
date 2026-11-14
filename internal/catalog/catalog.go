// Package catalog loads the sound index: builtins, user-added sounds and
// the remote trending index.json (cached, ETag-refreshed in background).
package catalog

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/okeyamy/faaa/internal/audio"
	"github.com/okeyamy/faaa/internal/paths"
)

type Sound struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Artist    string   `json:"artist,omitempty"`
	Source    string   `json:"source"` // meme (trending) | classic (built in) | mine
	Duration  float64  `json:"duration,omitempty"`
	URL       string   `json:"url,omitempty"`  // direct audio URL
	Page      string   `json:"page,omitempty"` // page URL (yt-dlp fallback)
	Tags      []string `json:"tags,omitempty"`
	TrendRank int      `json:"trend_rank,omitempty"`
}

type Index struct {
	Updated time.Time `json:"updated"`
	Sounds  []Sound   `json:"sounds"`
}

func cacheFile() string { return filepath.Join(paths.Cache(), "index.json") }
func etagFile() string  { return filepath.Join(paths.Cache(), "index.etag") }
func mineFile() string  { return filepath.Join(paths.Data(), "mine.json") }

// snapshot is the index at build time: works offline and before the first
// refresh. Rebuilt by `go run ./scraper`.
//
//go:embed index.json
var snapshot []byte

// Cached reads the local index copy (or the embedded snapshot) without
// touching the network.
func Cached() Index {
	var ix Index
	if b, err := os.ReadFile(cacheFile()); err == nil && json.Unmarshal(b, &ix) == nil && len(ix.Sounds) > 0 {
		return ix
	}
	_ = json.Unmarshal(snapshot, &ix)
	return ix
}

// Refresh does a conditional GET. changed=false on 304 or error.
func Refresh(url string) (ix Index, changed bool, err error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Cached(), false, err
	}
	if et, e := os.ReadFile(etagFile()); e == nil && fileExists(cacheFile()) {
		req.Header.Set("If-None-Match", string(et))
	}
	c := &http.Client{Timeout: 8 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return Cached(), false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return Cached(), false, nil
	}
	if resp.StatusCode != 200 {
		return Cached(), false, &http.ProtocolError{ErrorString: resp.Status}
	}
	if err := json.NewDecoder(resp.Body).Decode(&ix); err != nil {
		return Cached(), false, err
	}
	_ = os.MkdirAll(paths.Cache(), 0o755)
	b, _ := json.Marshal(ix)
	_ = os.WriteFile(cacheFile(), b, 0o644)
	if et := resp.Header.Get("ETag"); et != "" {
		_ = os.WriteFile(etagFile(), []byte(et), 0o644)
	}
	return ix, true, nil
}

// Mine returns user-added sounds.
func Mine() []Sound {
	var s []Sound
	if b, err := os.ReadFile(mineFile()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// AddMine records (or replaces) a user sound.
func AddMine(s Sound) error {
	list := Mine()
	out := list[:0]
	for _, x := range list {
		if x.ID != s.ID {
			out = append(out, x)
		}
	}
	out = append(out, s)
	_ = os.MkdirAll(paths.Data(), 0o755)
	b, _ := json.MarshalIndent(out, "", "  ")
	return os.WriteFile(mineFile(), b, 0o644)
}

func Builtins() []Sound {
	var out []Sound
	for _, b := range audio.Builtins() {
		out = append(out, Sound{ID: b.ID, Title: b.Title, Source: "classic"})
	}
	return out
}

// All merges mine, remote (by trend rank) and builtins; later dup ids drop.
func All(remote Index) []Sound {
	r := append([]Sound(nil), remote.Sounds...)
	sort.SliceStable(r, func(i, j int) bool {
		if a, b := srcOrder(r[i].Source), srcOrder(r[j].Source); a != b {
			return a < b
		}
		return rank(r[i]) < rank(r[j])
	})
	seen := map[string]bool{}
	var out []Sound
	for _, group := range [][]Sound{Mine(), Builtins(), r} {
		for _, s := range group {
			if !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, s)
			}
		}
	}
	return out
}

var order = map[string]int{"meme": 1}

func srcOrder(s string) int {
	if o, ok := order[s]; ok {
		return o
	}
	return 9
}

func rank(s Sound) int {
	if s.TrendRank == 0 {
		return 1 << 30
	}
	return s.TrendRank
}

// Recent returns cached live-search results.
func Recent() []Sound {
	var s []Sound
	if b, err := os.ReadFile(filepath.Join(paths.Cache(), "web.json")); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Find looks up a sound by id (catalog, then recent searches).
func Find(id string) (Sound, bool) {
	for _, s := range append(All(Cached()), Recent()...) {
		if s.ID == id {
			return s, true
		}
	}
	return Sound{}, false
}

// lastFile holds the ordered result set of the most recent search, so the row
// numbers shown to the user stay valid for `faaa set 2` / `faaa play 2`.
func lastFile() string { return filepath.Join(paths.Cache(), "last.json") }

// Listing returns the last search result set, in display order.
func Listing() []Sound {
	var s []Sound
	if b, err := os.ReadFile(lastFile()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// SaveListing replaces the last search result set.
func SaveListing(s []Sound) error {
	_ = os.MkdirAll(paths.Cache(), 0o755)
	b, _ := json.Marshal(s)
	return os.WriteFile(lastFile(), b, 0o644)
}

// Resolve turns what the user typed into a sound. Exact ids win (hooks, `.faaa`
// and older scripts rely on that), then a row number from the last listing,
// then a unique case-insensitive id prefix, then a title match. Titles are
// compared normalized: exact first, then contiguous, then the same words in any
// order, then every query word against a title word (partials allowed).
//
// When several sounds match, the one from the static catalog wins over a
// live-search duplicate with the same title; anything still tied returns
// candidates (max five) and ok=false so nothing gets armed.
func Resolve(q string) (s Sound, candidates []Sound, ok bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return Sound{}, nil, false
	}
	all := All(Cached())
	pool := append(append([]Sound{}, all...), Recent()...)
	canonical := make(map[string]bool, len(all))
	for _, x := range all {
		canonical[x.ID] = true
	}
	settle := func(m []Sound) (Sound, []Sound, bool) {
		if len(m) == 1 {
			return m[0], nil, true
		}
		var canon []Sound
		for _, x := range m {
			if canonical[x.ID] {
				canon = append(canon, x)
			}
		}
		if len(canon) == 1 {
			return canon[0], nil, true
		}
		return Sound{}, m, false
	}
	for _, x := range pool {
		if x.ID == q {
			return x, nil, true
		}
	}
	if n, err := strconv.Atoi(q); err == nil && n > 0 {
		if last := Listing(); n <= len(last) {
			return last[n-1], nil, true
		}
	}
	needle := strings.ToLower(q)
	if m := pick(pool, func(x Sound) bool { return strings.HasPrefix(strings.ToLower(x.ID), needle) }); len(m) > 0 {
		return settle(m)
	}
	nq := norm(q)
	if nq == "" {
		return Sound{}, nil, false
	}
	if m := pick(pool, func(x Sound) bool { return norm(x.Title) == nq }); len(m) > 0 {
		return settle(m)
	}
	if m := pick(pool, func(x Sound) bool { return strings.Contains(norm(x.Title), nq) }); len(m) > 0 {
		return settle(m)
	}
	qt := strings.Fields(nq)
	if len(qt) == 0 {
		return Sound{}, nil, false
	}
	// Same words in any order ("Boom Vine" == "Vine Boom").
	if m := pick(pool, func(x Sound) bool { return sameTokens(qt, strings.Fields(norm(x.Title))) }); len(m) > 0 {
		return settle(m)
	}
	// Every query word matches a title word, partial words included.
	if m := pick(pool, func(x Sound) bool { return tokensMatch(qt, strings.Fields(norm(x.Title))) }); len(m) > 0 {
		return settle(m)
	}
	return Sound{}, nil, false
}

// norm flattens case, punctuation and whitespace so titles compare by words
// rather than by typography.
func norm(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// tokensMatch reports whether every query word equals or prefixes a title word.
func tokensMatch(query, title []string) bool {
	for _, q := range query {
		ok := false
		for _, t := range title {
			if t == q || strings.HasPrefix(t, q) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// sameTokens reports whether both sides contain the same words, order aside.
func sameTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, q := range a {
		found := false
		for i, t := range b {
			if !used[i] && t == q {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// pick filters pool, de-duplicating by id, and keeps at most five matches so an
// ambiguous query never dumps the whole catalog.
func pick(pool []Sound, f func(Sound) bool) []Sound {
	seen := map[string]bool{}
	var out []Sound
	for _, s := range pool {
		if seen[s.ID] || !f(s) {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
		if len(out) == 5 {
			break
		}
	}
	return out
}

// Ready reports whether the decoded WAV exists locally.
func Ready(id string) bool { return fileExists(paths.Sound(id)) }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
