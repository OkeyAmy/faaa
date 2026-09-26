// Command scraper builds index.json from trending sources. Runs in CI
// (daily GitHub Action), never on users' machines, so when a source changes
// only this file needs fixing. It stores metadata + links only, no audio.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/okeyamy/faaa/internal/catalog"
)

const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

var client = &http.Client{Timeout: 20 * time.Second}

func get(url string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// source returns sounds in rank order.
type source struct {
	name string
	run  func() ([]catalog.Sound, error)
}

// Short viral sounds only (no music tracks).
var sources = []source{
	{"meme", myinstants},
}

// regions whose trending lists we merge: what's viral differs by country.
var regions = []string{"us", "gb", "ng", "in", "br", "de", "fr", "es", "mx", "ph", "ca", "au"}

// --- myinstants: short meme sounds with direct mp3 links -------------------

var miRe = regexp.MustCompile(`(?s)play\('(/media/sounds/[^']+)'[^)]*\).*?<a href="/en/instant/([^/"]+)/"[^>]*class="instant-link[^"]*">([^<]+)</a>`)

func myinstants() ([]catalog.Sound, error) {
	var out []catalog.Sound
	seen := map[string]bool{}
	var pages []string
	for _, r := range regions { // round-robin: each region's #1 before anyone's #2
		pages = append(pages, "https://www.myinstants.com/en/trending/"+r+"/")
	}
	pages = append(pages, "https://www.myinstants.com/en/index/us/", "https://www.myinstants.com/en/index/us/?page=2")
	type hit struct {
		s   catalog.Sound
		pos int
	}
	var hits []hit
	for _, page := range pages {
		b, err := get(page)
		if err != nil {
			log.Printf("myinstants: %v", err)
			continue
		}
		for pos, m := range miRe.FindAllStringSubmatch(string(b), -1) {
			id := "m-" + slug(m[2])
			if seen[id] {
				continue
			}
			seen[id] = true
			hits = append(hits, hit{catalog.Sound{
				ID: id, Title: strings.TrimSpace(html.UnescapeString(m[3])), Source: "meme",
				URL: "https://www.myinstants.com" + m[1], Page: "https://www.myinstants.com/en/instant/" + m[2] + "/",
			}, pos})
		}
	}
	// rank by best position in any region's list (stable: earlier pages win ties)
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	for _, h := range hits {
		h.s.TrendRank = len(out) + 1
		out = append(out, h.s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no sounds parsed (markup changed?)")
	}
	return out, nil
}

func getJSON(url string, v any) error {
	b, err := get(url)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// --- community: reviewed submissions under community/*.json ----------------

func community(dir string) []catalog.Sound {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []catalog.Sound
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s []catalog.Sound
		if err := json.Unmarshal(b, &s); err != nil {
			log.Printf("community %s: %v", f, err)
			continue
		}
		for _, x := range s {
			x.Source = "meme"
			out = append(out, x)
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func main() {
	out := flag.String("o", "internal/catalog/index.json", "output file (embedded in the binary + served raw)")
	comm := flag.String("community", "community", "community submissions dir")
	flag.Parse()

	// Keep last good data per source so one broken scraper doesn't wipe it.
	var prev catalog.Index
	if b, err := os.ReadFile(*out); err == nil {
		_ = json.Unmarshal(b, &prev)
	}

	results := make([][]catalog.Sound, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.run()
			if err != nil || len(got) == 0 {
				log.Printf("%s failed (%v); keeping previous", s.name, err)
				for _, p := range prev.Sounds {
					if p.Source == s.name && !strings.HasPrefix(p.ID, "c-") {
						got = append(got, p)
					}
				}
			}
			log.Printf("%s: %d sounds", s.name, len(got))
			results[i] = got
		}()
	}
	wg.Wait()

	ix := catalog.Index{Updated: time.Now().UTC()}
	ix.Sounds = append(ix.Sounds, community(*comm)...)
	for _, r := range results {
		ix.Sounds = append(ix.Sounds, r...)
	}
	b, _ := json.MarshalIndent(ix, "", " ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s: %d sounds, %d KB", *out, len(ix.Sounds), len(b)/1024)
}
