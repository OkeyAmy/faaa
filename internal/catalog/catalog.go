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
	"time"

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

// Ready reports whether the decoded WAV exists locally.
func Ready(id string) bool { return fileExists(paths.Sound(id)) }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
