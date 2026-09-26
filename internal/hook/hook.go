// Package hook installs global git hooks that play a sound after a
// successful push.
//
// Git has no post-push hook. pre-push hands the pushed shas to a detached
// `faaa _pushed`, which waits for the git push process to exit and plays
// only if the remote-tracking refs moved to those shas (see push.go).
// Nothing runs on commit/checkout/status/fetch.
//
// Two install modes:
//   - config (git with hook.<name>.event): one global pre-push config hook
//     that runs alongside .git/hooks, husky etc.
//   - hookspath (older git): global core.hooksPath replaces .git/hooks, so
//     every standard hook gets a shim chaining to the repo's own hook and
//     any previous global hooks path.
package hook

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/okeyamy/faaa/internal/paths"
)

var names = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit",
	"pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push", "pre-receive",
	"update", "post-receive", "post-update",
	"pre-auto-gc", "post-rewrite", "sendemail-validate",
	// not push-to-checkout: its mere presence replaces git's own worktree update.
	// not reference-transaction / post-index-change: they fire many times per
	// commit/checkout, and a shim costs ~1ms each. `faaa doctor` flags repos
	// that actually use them.
}

// Unshimmed hooks bypassed in hookspath mode.
var Unshimmed = []string{"reference-transaction", "post-index-change"}

// stdin-consuming hooks: input must be captured and replayed to each chained hook.
var stdinHooks = map[string]bool{
	"pre-push": true, "pre-receive": true, "post-receive": true, "post-rewrite": true,
}

// config hook names (hook.<name>.event)
var configHooks = map[string]string{"faaa-push": "pre-push"}

const marker = "# faaa hook shim"

func q(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// hooks run at the worktree root: skip forking git in the common case.
const gitDir = `if [ -z "${GIT_DIR:-}" ] && [ -d .git ]; then d=.git; else d=$(git rev-parse --git-common-dir 2>/dev/null) || d=.git; fi` + "\n"

// readStdin captures stdin with shell builtins only (no cat fork).
const readStdin = `in=; while IFS= read -r x || [ -n "$x" ]; do in="$in$x
"; done` + "\n"

// Script renders the hook for one name. chain=false (config mode) emits
// the standalone pre-push; chain=true emits a hooksPath shim chaining to
// the repo's own hook and to prev (a previous global hooks dir, may be "").
// $PPID is the git push process: faaa waits for it to exit.
func Script(name, faaa, prev string, chain bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n%s — edit via `faaa install`\n", marker)
	if !chain {
		fmt.Fprintf(&b, "exec %s _pushed \"$PPID\" \"$@\"\n", q(faaa))
		return b.String()
	}
	b.WriteString(gitDir)
	fmt.Fprintf(&b, "l=\"$d/hooks/%s\"; [ -x \"$l\" ] || l=\n", name)
	consumers := `"$l"`
	if prev != "" {
		fmt.Fprintf(&b, "p=%s; [ -x \"$p\" ] || p=\n", q(filepath.Join(prev, name)))
		consumers = `"$l$p"`
	}
	if name != "pre-push" {
		fmt.Fprintf(&b, "[ -n %s ] || exit 0\n", consumers)
	}
	run := `"$h" "$@" || exit $?`
	if stdinHooks[name] {
		b.WriteString(readStdin)
		run = `printf '%s' "$in" | "$h" "$@" || exit $?`
	}
	fmt.Fprintf(&b, "h=$l; [ -n \"$h\" ] && { %s; }\n", run)
	if prev != "" {
		fmt.Fprintf(&b, "h=$p; [ -n \"$h\" ] && { %s; }\n", run)
	}
	if name == "pre-push" {
		fmt.Fprintf(&b, "printf '%%s' \"$in\" | %s _pushed \"$PPID\" \"$@\"\n", q(faaa))
	}
	b.WriteString("exit 0\n")
	return b.String()
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// GlobalHooksPath returns the current global core.hooksPath ("" if unset),
// with ~ expanded.
func GlobalHooksPath() string {
	v, _ := git("config", "--global", "--type=path", "--get", "core.hooksPath")
	return v
}

// rawGlobalHooksPath is the value as the user wrote it (for restoring).
func rawGlobalHooksPath() string {
	v, _ := git("config", "--global", "--get", "core.hooksPath")
	return v
}

// ConfigHooks reports whether git supports hook.<name>.event config hooks.
func ConfigHooks() bool {
	if os.Getenv("FAAA_HOOKSPATH") == "1" { // force legacy mode (tests)
		return false
	}
	// `git hook list` needs a repo, so probe inside a throwaway one.
	dir, err := os.MkdirTemp("", "faaa-probe")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	if _, err := git("init", "-q", dir); err != nil {
		return false
	}
	out, err := git("-C", dir, "-c", "hook.faaa-probe.event=pre-push", "-c", "hook.faaa-probe.command=true",
		"hook", "list", "pre-push")
	return err == nil && strings.Contains(out, "faaa-probe")
}

// Install writes hooks and wires them into global git config. Returns the
// previous global hooksPath to save (only replaced in hookspath mode).
func Install(faaa, prevSaved string) (prev string, mode string, err error) {
	dir := paths.Hooks()
	ours := GlobalHooksPath() == dir
	prev = rawGlobalHooksPath()
	if ours {
		prev = prevSaved // reinstall: keep the originally saved one
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}

	if ConfigHooks() {
		if ours { // migrating from hookspath mode: give the user theirs back
			if err := restoreHooksPath(prev); err != nil {
				return "", "", err
			}
		}
		for name, event := range configHooks {
			f := filepath.Join(dir, event)
			if err := os.WriteFile(f, []byte(Script(event, faaa, "", false)), 0o755); err != nil {
				return "", "", err
			}
			_, _ = git("config", "--global", "--unset-all", "hook."+name+".event")
			if _, err := git("config", "--global", "hook."+name+".event", event); err != nil {
				return "", "", fmt.Errorf("set hook.%s: %w", name, err)
			}
			if _, err := git("config", "--global", "hook."+name+".command", command(f)); err != nil {
				return "", "", err
			}
		}
		return "", "config", nil
	}

	removeConfigHooks()
	chain := expand(prev)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(Script(n, faaa, chain, true)), 0o755); err != nil {
			return "", "", err
		}
	}
	if _, err := git("config", "--global", "core.hooksPath", dir); err != nil {
		return "", "", fmt.Errorf("set core.hooksPath: %w", err)
	}
	return prev, "hookspath", nil
}

// command renders a hook command. A path without shell metacharacters is
// exec'd directly by git; quoting it would force an extra `sh -c` (~0.6ms)
// on every ref update. Forward slashes: runs under git-bash on Windows.
func command(path string) string {
	p := filepath.ToSlash(path)
	if strings.ContainsAny(p, "|&;<>()$`\\\"' \t\n*?[#~=%") {
		return q(p)
	}
	return p
}

// expand resolves a leading ~ like git's --type=path.
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

// Target returns the faaa binary path baked into the hooks.
func Target() string {
	b, err := os.ReadFile(filepath.Join(paths.Hooks(), "pre-push"))
	if err != nil {
		return ""
	}
	j := bytes.Index(b, []byte("' _pushed"))
	if j < 0 {
		return ""
	}
	i := bytes.LastIndex(b[:j], []byte(" '"))
	if i < 0 {
		return ""
	}
	rest := b[i+2:]
	j -= i + 2
	if j < 0 {
		return ""
	}
	return strings.ReplaceAll(string(rest[:j]), `'\''`, "'")
}

func restoreHooksPath(prev string) error {
	var err error
	if prev != "" {
		_, err = git("config", "--global", "core.hooksPath", prev)
	} else {
		_, err = git("config", "--global", "--unset", "core.hooksPath")
	}
	return err
}

func removeConfigHooks() {
	for name := range configHooks {
		_, _ = git("config", "--global", "--remove-section", "hook."+name)
	}
}

// Uninstall removes config hooks, restores a replaced hooksPath, deletes files.
func Uninstall(prev string) error {
	removeConfigHooks()
	if GlobalHooksPath() == paths.Hooks() {
		if err := restoreHooksPath(prev); err != nil {
			return err
		}
	}
	return os.RemoveAll(paths.Hooks())
}

// Installed reports whether our hooks are live globally.
func Installed() bool {
	b, err := os.ReadFile(filepath.Join(paths.Hooks(), "pre-push"))
	if err != nil || !bytes.Contains(b, []byte(marker)) {
		return false
	}
	if GlobalHooksPath() == paths.Hooks() {
		return true
	}
	v, _ := git("config", "--global", "--get", "hook.faaa-push.command")
	return v != ""
}

// Mode reports the active install mode ("config", "hookspath" or "").
func Mode() string {
	if !Installed() {
		return ""
	}
	if GlobalHooksPath() == paths.Hooks() {
		return "hookspath"
	}
	return "config"
}

// RepoOverride returns a repo-local core.hooksPath (e.g. husky). Only
// matters in hookspath mode, where it bypasses the global shims.
func RepoOverride() (string, error) {
	if _, err := git("rev-parse", "--git-dir"); err != nil {
		return "", errors.New("not in a git repo")
	}
	v, _ := git("config", "--local", "--get", "core.hooksPath")
	return v, nil
}
