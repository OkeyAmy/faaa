// Package fetch downloads a sound and normalises it into the local cache.
package fetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/okeyamy/faaa/internal/audio"
	"github.com/okeyamy/faaa/internal/catalog"
	"github.com/okeyamy/faaa/internal/paths"
	"github.com/okeyamy/faaa/internal/tools"
)

// Src is where a sound's original download is kept, so changing the clip
// length re-cuts locally instead of downloading again.
func Src(id string) string { return filepath.Join(paths.Cache(), "src", id) }

// client is shared so every fetch reuses one warm TLS connection per host
// (a fresh handshake to the sound host costs ~0.5s).
var client = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true,
		MaxIdleConnsPerHost: 8, IdleConnTimeout: 2 * time.Minute,
		// fail fast on unreachable hosts instead of hanging the whole timeout
		DialContext:           (&net.Dialer{Timeout: 6 * time.Second}).DialContext,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
}

// Warm opens the connection to the sound host ahead of the first preview.
func Warm() {
	req, _ := http.NewRequest("HEAD", "https://www.myinstants.com/", nil)
	req.Header.Set("User-Agent", ua)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}

var locks sync.Map // id -> *sync.Mutex: one fetch per sound at a time

// Ensure makes sure the sound's WAV exists, downloading/decoding if needed.
// Concurrent calls for the same id (prefetch + play) share one fetch.
func Ensure(s catalog.Sound, maxSec float64) error {
	dst := paths.Sound(s.ID)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	mu, _ := locks.LoadOrStore(s.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if _, err := os.Stat(dst); err == nil {
		return nil // another caller just finished it
	}
	return Recut(s, maxSec)
}

// Recut (re)builds the WAV from the kept original, fetching it if missing.
func Recut(s catalog.Sound, maxSec float64) error {
	dst := paths.Sound(s.ID)
	if audio.IsBuiltin(s.ID) {
		return audio.WriteBuiltin(s.ID, dst, maxSec)
	}
	src := Src(s.ID)
	if _, err := os.Stat(src); err != nil {
		p, err := Download(s.URL, s.Page, s.ID)
		if err != nil {
			return err
		}
		_ = os.MkdirAll(filepath.Dir(src), 0o755)
		if err := os.Rename(p, src); err != nil {
			return err
		}
	}
	_, err := audio.Convert(src, dst, maxSec)
	return err
}

// Download fetches a direct audio URL, or falls back to yt-dlp for page URLs.
func Download(direct, page, id string) (string, error) {
	tmp := filepath.Join(paths.Cache(), "dl")
	_ = os.MkdirAll(tmp, 0o755)
	if direct != "" {
		p, err := httpGet(direct, filepath.Join(tmp, id))
		if err == nil || page == "" {
			return p, err
		}
	}
	if page == "" {
		return "", errors.New("sound has no URL")
	}
	return ytdlp(page, filepath.Join(tmp, id))
}

const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

func httpGet(url, dst string) (string, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// 20MB cap: these are meant to be short clips.
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 20<<20)); err != nil {
		return "", err
	}
	return dst, nil
}

func ytdlp(page, dst string) (string, error) {
	_, p, err := FromLink(page, dst, nil)
	return p, err
}

var tiktokRe = regexp.MustCompile(`(?i)^https?://([a-z]+\.)?tiktok\.com/`)

// tiktokSound resolves a TikTok video link to its sound's mp3 via the
// tikwm API: no tools, ~100KB. Returns title (sound name - author).
func tiktokSound(link, dst string) (title, path string, err error) {
	req, _ := http.NewRequest("POST", "https://www.tikwm.com/api/", strings.NewReader("url="+url.QueryEscape(link)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Music     string `json:"music"`
			MusicInfo struct {
				Title  string `json:"title"`
				Author string `json:"author"`
				Play   string `json:"play"`
			} `json:"music_info"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", fmt.Errorf("tiktok lookup failed: %w", err)
	}
	if r.Code != 0 {
		return "", "", fmt.Errorf("couldn't read that TikTok (%s)", r.Msg)
	}
	// TikTok serves the same sound from several CDN hosts; try each.
	var p string
	err = errors.New("that TikTok has no downloadable sound")
	for _, u := range []string{r.Data.MusicInfo.Play, r.Data.Music} {
		if u == "" {
			continue
		}
		if p, err = httpGet(u, dst+".mp3"); err == nil {
			break
		}
	}
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return "", "", errors.New("TikTok's servers aren't reachable from this network (blocked or down) — try another network, or save the audio and run: faaa add ./file.mp3")
		}
		return "", "", err
	}
	title = r.Data.MusicInfo.Title
	if a := r.Data.MusicInfo.Author; strings.HasPrefix(strings.ToLower(title), "original sound") && a != "" {
		title = a + " (original sound)"
	}
	return title, p, nil
}

// FromLink pulls the audio of any video/post link yt-dlp understands
// (short-video apps, YouTube, SoundCloud, …) into dst.<ext>. Fetches
// yt-dlp + ffmpeg once if missing; note reports that.
func FromLink(link, dst string, note func(string)) (title, path string, err error) {
	if tiktokRe.MatchString(link) {
		if strings.Contains(link, "/music/") {
			// sound pages sit behind TikTok's signed API; videos don't
			return "", "", errors.New("that's a TikTok sound page — open any video that uses the sound, tap Share → Copy link, and paste that")
		}
		t, p, err := tiktokSound(link, dst)
		if err == nil {
			return t, p, nil
		}
		return "", "", err
	}
	y, err := tools.Ensure("yt-dlp", note)
	if err != nil {
		return "", "", err
	}
	ff, err := tools.Ensure("ffmpeg", note) // their audio is AAC/Opus
	if err != nil {
		return "", "", err
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	cmd := exec.Command(y, "--no-playlist", "--no-warnings", "-q", "--max-filesize", "60M",
		"--ffmpeg-location", ff, "-f", "bestaudio/best", "-o", dst+".%(ext)s",
		"--print", "after_move:%(title)s\t%(filepath)s", link)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "ERROR:"); i >= 0 {
			msg = msg[i:]
		}
		return "", "", fmt.Errorf("couldn't get audio from that link: %s", trunc(msg, 160))
	}
	line := strings.TrimSpace(string(b))
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = line[i+1:]
	}
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		return "", "", errors.New("yt-dlp gave no file")
	}
	return parts[0], parts[1], nil
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

var directAudio = regexp.MustCompile(`(?i)\.(mp3|wav|m4a|ogg|opus|aac)(\?|$)`)

// Add imports the user's own sound from a file path or any link, cuts the
// best part, and records it under "mine". name may be "" (derived).
func Add(src, name string, maxSec float64, note func(string)) (catalog.Sound, audio.Clip, error) {
	var s catalog.Sound
	tmp := filepath.Join(paths.Cache(), "dl", fmt.Sprintf("add-%d", time.Now().UnixNano()))
	_ = os.MkdirAll(filepath.Dir(tmp), 0o755)
	var file string
	switch {
	case tools.IsLink(src) && directAudio.MatchString(src):
		p, err := httpGet(src, tmp)
		if err != nil {
			return s, audio.Clip{}, err
		}
		file, s.Page = p, src
		if name == "" {
			u := strings.Split(src, "?")[0]
			name = strings.TrimSuffix(filepath.Base(u), filepath.Ext(u))
		}
	case tools.IsLink(src):
		title, p, err := FromLink(src, tmp, note)
		if err != nil {
			return s, audio.Clip{}, err
		}
		file, s.Page = p, src
		if name == "" {
			name = title
		}
	default:
		b, err := os.ReadFile(src)
		if err != nil {
			return s, audio.Clip{}, err
		}
		file = tmp + filepath.Ext(src)
		if err := os.WriteFile(file, b, 0o644); err != nil {
			return s, audio.Clip{}, err
		}
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
		}
	}
	if slug(name) == "" {
		name = "sound"
	}
	s.ID, s.Title, s.Source = "mine-"+slug(name), name, "mine"
	orig := Src(s.ID)
	_ = os.MkdirAll(filepath.Dir(orig), 0o755)
	if err := os.Rename(file, orig); err != nil {
		return s, audio.Clip{}, err
	}
	c, err := audio.Convert(orig, paths.Sound(s.ID), maxSec)
	if err != nil {
		return s, c, err
	}
	s.Duration = c.Full
	return s, c, catalog.AddMine(s)
}
