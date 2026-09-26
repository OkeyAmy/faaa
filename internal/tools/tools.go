// Package tools finds helper binaries (ffmpeg, yt-dlp) on PATH. Only
// yt-dlp is ever fetched, and only as its 3MB Python zipapp when python3 is
// present: no silent multi-MB downloads.
package tools

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/okeyamy/faaa/internal/paths"
)

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func cached(name string) string { return filepath.Join(paths.Cache(), "bin", exe(name)) }

// Find returns a usable path without downloading ("" if none).
func Find(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if _, err := os.Stat(cached(name)); err == nil {
		return cached(name)
	}
	return ""
}

var hint = map[string]string{
	"darwin":  "brew install %s",
	"linux":   "install %s with your package manager (apt/dnf/pacman)",
	"windows": "winget install %s",
}

func installHint(name string) string {
	h := hint[runtime.GOOS]
	if h == "" {
		h = "install %s"
	}
	return fmt.Sprintf(h, name)
}

// Ensure returns a path to the tool. yt-dlp is fetched once (3MB) if
// python3 exists; anything else asks the user to install it.
func Ensure(name string, note func(string)) (string, error) {
	if p := Find(name); p != "" {
		return p, nil
	}
	py, err := exec.LookPath("python3")
	if name != "yt-dlp" || err != nil {
		return "", fmt.Errorf("this link needs %s — %s", name, installHint(name))
	}
	if note != nil {
		note("one-time setup: fetching yt-dlp (3MB)…")
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get("https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch yt-dlp: %s", resp.Status)
	}
	dir := filepath.Join(paths.Cache(), "bin")
	_ = os.MkdirAll(dir, 0o755)
	app := filepath.Join(dir, "yt-dlp.pyz")
	f, err := os.Create(app + ".part")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 20<<20)); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	if err := os.Rename(app+".part", app); err != nil {
		return "", err
	}
	// tiny launcher so callers exec one path on every OS
	dst := cached(name)
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("this link needs yt-dlp — %s", installHint("yt-dlp"))
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %q %q \"$@\"\n", py, app)
	return dst, os.WriteFile(dst, []byte(script), 0o755)
}

// IsLink reports whether s looks like a web link rather than a search.
func IsLink(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
