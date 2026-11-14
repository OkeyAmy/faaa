// Package upgrade answers one question: is there a newer faaa release, and how
// would I install it? It is strictly read-only — it never downloads, writes,
// chmods or renames anything. Shipping a release is how updates arrive; this
// command only points at the right installer.
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

const (
	apiURL = "https://api.github.com/repos/OkeyAmy/faaa/releases/latest"
	userUA = "faaa-upgrade"

	directCommand = "curl -fsSL https://raw.githubusercontent.com/OkeyAmy/faaa/main/install.sh | sh"
	npmCommand    = "npm i -g @okeyamy/faaa@latest"
	brewCommand   = "brew upgrade faaa"
)

var client = &http.Client{Timeout: 20 * time.Second}

// Detect says how this faaa was installed: a package manager knows its own
// upgrade command, everything else uses the install script. Paths are
// compared lowercased with forward slashes so Windows separators work too.
func Detect(exe string) string {
	p := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	switch {
	case strings.Contains(p, "node_modules"):
		return "npm"
	case strings.Contains(p, "cellar"), strings.Contains(p, "homebrew"), strings.Contains(p, "linuxbrew"):
		return "brew"
	}
	return "direct"
}

// Hint is the exact command that installs a newer release for method.
func Hint(method string) string {
	switch method {
	case "npm":
		return npmCommand
	case "brew":
		return brewCommand
	}
	return directCommand
}

// Compare orders two versions: -1 if a < b, 0 if equal, 1 if a > b.
// It accepts "v0.0.7" and "0.0.7", compares dot-separated numbers (so 0.0.10
// is newer than 0.0.9), and treats a pre-release ("0.0.7-rc1") as older than
// the release it leads to.
func Compare(a, b string) int {
	ac, ap := parseVersion(a)
	bc, bp := parseVersion(b)
	n := len(ac)
	if len(bc) > n {
		n = len(bc)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "": // a is the real release, b is a pre-release
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	}
	return 1
}

// parseVersion splits a tag into numeric core parts and a pre-release suffix.
// Anything unparseable in a core part counts as zero.
func parseVersion(v string) (core []int, pre string) {
	v = strings.TrimSpace(v)
	v = trimV(v)
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	for _, f := range strings.Split(v, ".") {
		core = append(core, leadingInt(f))
	}
	return core, pre
}

func leadingInt(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			break
		}
	}
	return n
}

func trimV(v string) string {
	v = strings.TrimPrefix(v, "v")
	return strings.TrimPrefix(v, "V")
}

// resolve fills in the version for builds that carry no ldflag. `go install`
// reports "dev" in main.version but the toolchain embeds the module version,
// so those builds still compare correctly.
func resolve(ldflag string) string {
	v := strings.TrimSpace(ldflag)
	if v != "" && v != "dev" && v != "(devel)" {
		return trimV(v)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if mv := strings.TrimSpace(info.Main.Version); mv != "" && mv != "(devel)" {
			return trimV(mv)
		}
	}
	if v == "" {
		return "dev"
	}
	return v
}

// Check reports the latest release and whether it is newer than current.
// It only reads: one request to the GitHub releases API, no files touched.
func Check(current string) (latest string, newer bool, err error) {
	rel, err := fetchLatest()
	if err != nil {
		return "", false, err
	}
	return rel.version(), Compare(rel.version(), resolve(current)) > 0, nil
}

type release struct {
	TagName string `json:"tag_name"`
}

func (r *release) version() string {
	return trimV(strings.TrimSpace(r.TagName))
}

func fetchLatest() (*release, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, errors.New("couldn't check for updates — try again")
	}
	req.Header.Set("User-Agent", userUA)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, networkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("couldn't check for updates — GitHub answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, networkError(err)
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		return nil, errors.New("couldn't understand GitHub's answer — try again later")
	}
	return &rel, nil
}

// networkError turns Go's transport detail ("dial tcp 127.0.0.1:9: connect:
// connection refused") into something a person can act on.
func networkError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || osIsTimeout(err) {
		return errors.New("couldn't check for updates — GitHub timed out, try again in a moment")
	}
	return errors.New("couldn't check for updates — check your network and try again")
}

func osIsTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	if t, ok := err.(timeout); ok {
		return t.Timeout()
	}
	return false
}
