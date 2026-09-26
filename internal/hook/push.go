package hook

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Check is one pushed ref: after a successful push, git moves Tracking
// (refs/remotes/<remote>/<branch>) from Before to Sha.
type Check struct {
	Tracking, Sha, Before string
}

const zero = "0000000000000000000000000000000000000000"

// ParsePrePush turns pre-push stdin into checks. Refs without a tracking
// ref (tags, refs/for/*, pushes to a raw URL) are skipped; if nothing is
// verifiable the caller plays optimistically.
func ParsePrePush(r io.Reader, remote, url string) (checks []Check, pushed bool) {
	named := remote != "" && remote != url
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text()) // <local ref> <local sha> <remote ref> <remote sha>
		if len(f) != 4 {
			continue
		}
		pushed = true
		if named && strings.HasPrefix(f[2], "refs/heads/") {
			checks = append(checks, Check{Tracking: "refs/remotes/" + remote + "/" + strings.TrimPrefix(f[2], "refs/heads/"), Sha: f[1]})
		}
	}
	return checks, pushed
}

// refValue returns the sha a ref points at ("" if missing).
func refValue(ref string) string {
	out, err := exec.Command("git", "rev-parse", "-q", "--verify", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Snapshot records current tracking values so a no-op can't look like success.
func Snapshot(cs []Check) {
	for i := range cs {
		cs[i].Before = refValue(cs[i].Tracking)
	}
}

// Succeeded reports whether any tracking ref moved to the pushed sha
// (or vanished, for a delete).
func Succeeded(cs []Check) bool {
	for _, c := range cs {
		now := refValue(c.Tracking)
		if c.Sha == zero {
			if c.Before != "" && now == "" {
				return true
			}
		} else if now == c.Sha && c.Before != c.Sha {
			return true
		}
	}
	return false
}

// Encode/Decode pass checks to the detached waiter via argv.
func Encode(cs []Check) []string {
	var a []string
	for _, c := range cs {
		a = append(a, c.Tracking+"="+c.Sha+"="+c.Before)
	}
	return a
}

func Decode(args []string) []Check {
	var cs []Check
	for _, a := range args {
		p := strings.SplitN(a, "=", 3)
		if len(p) == 3 {
			cs = append(cs, Check{p[0], p[1], p[2]})
		}
	}
	return cs
}

// Await blocks until the git push process exits, then reports success.
// With no checks (nothing verifiable) it reports success after the wait.
// Unix: wait on the pid. Elsewhere (no reliable pid through git-bash):
// poll the refs for up to 90s.
func Await(pid string, cs []Check) bool {
	if runtime.GOOS != "windows" {
		if p, err := strconv.Atoi(pid); err == nil && p > 1 {
			deadline := time.Now().Add(30 * time.Minute)
			for alive(p) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			return len(cs) == 0 || Succeeded(cs)
		}
	}
	if len(cs) == 0 {
		return true
	}
	for end := time.Now().Add(90 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if Succeeded(cs) {
			return true
		}
	}
	return false
}

// Background re-execs self detached so the hook returns immediately.
func Background(self string, args ...string) error {
	c := exec.Command(self, args...)
	c.Stdin, c.Stdout, c.Stderr = nil, nil, nil
	if wd, err := os.Getwd(); err == nil {
		c.Dir = wd
	}
	detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}
