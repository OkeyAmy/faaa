// Package search finds short sounds beyond the catalog, live, via
// Myinstants search. Results download on first play like catalog entries.
package search

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/okeyamy/faaa/internal/catalog"
	"github.com/okeyamy/faaa/internal/paths"
)

const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

var client = &http.Client{Timeout: 6 * time.Second}

func get(u string) ([]byte, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// Web searches the first two result pages in parallel.
func Web(q string) ([]catalog.Sound, error) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		return nil, nil
	}
	pages := make([][]catalog.Sound, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range pages {
		wg.Add(1)
		go func() { defer wg.Done(); pages[i], errs[i] = myinstants(q, i+1) }()
	}
	wg.Wait()
	var out []catalog.Sound
	seen := map[string]bool{}
	for _, p := range pages {
		for _, s := range p {
			if !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 && errs[0] != nil {
		return nil, errs[0]
	}
	remember(out)
	return out, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

var miRe = regexp.MustCompile(`(?s)play\('(/media/sounds/[^']+)'[^)]*\).*?<a href="/en/instant/([^/"]+)/"[^>]*class="instant-link[^"]*">([^<]+)</a>`)

func myinstants(q string, page int) ([]catalog.Sound, error) {
	b, err := get(fmt.Sprintf("https://www.myinstants.com/en/search/?name=%s&page=%d", url.QueryEscape(q), page))
	if err != nil {
		return nil, err
	}
	var out []catalog.Sound
	for _, m := range miRe.FindAllStringSubmatch(string(b), -1) {
		out = append(out, catalog.Sound{
			ID: "m-" + slug(m[2]), Title: strings.TrimSpace(html.UnescapeString(m[3])), Source: "meme",
			URL: "https://www.myinstants.com" + m[1], Page: "https://www.myinstants.com/en/instant/" + m[2] + "/",
		})
	}
	return out, nil
}

// remember keeps recent web results so `faaa set <id>` works after a search.
func remember(s []catalog.Sound) {
	f := filepath.Join(paths.Cache(), "web.json")
	old := catalog.Recent()
	seen := map[string]bool{}
	var all []catalog.Sound
	for _, x := range append(s, old...) {
		if !seen[x.ID] && len(all) < 300 {
			seen[x.ID] = true
			all = append(all, x)
		}
	}
	_ = os.MkdirAll(paths.Cache(), 0o755)
	b, _ := json.Marshal(all)
	_ = os.WriteFile(f, b, 0o644)
}
