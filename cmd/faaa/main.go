// faaa: play a fun sound every time you git push.
package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/okeyamy/faaa/internal/audio"
	"github.com/okeyamy/faaa/internal/catalog"
	"github.com/okeyamy/faaa/internal/config"
	"github.com/okeyamy/faaa/internal/fetch"
	"github.com/okeyamy/faaa/internal/hook"
	"github.com/okeyamy/faaa/internal/paths"
	"github.com/okeyamy/faaa/internal/search"
	"github.com/okeyamy/faaa/internal/tools"
	"github.com/okeyamy/faaa/internal/tui"
)

var version = "dev"

const usage = `faaa — git push, but louder

usage:
  faaa                    open the sound picker
  faaa install            arm git (automatic on first launch)
  faaa uninstall          disarm, restore previous git config
  faaa update             refresh the sound list now (auto every launch)
  faaa play [id]          play current (or given) sound
  faaa set <id>           choose push sound
  faaa add <file|link> [name] your own sound: a file, or a link to any
                          TikTok (no setup) / Reels / Shorts / YouTube post
  faaa search <words>     find any sound online
  faaa length [sec]       clip length (default 15, max 60) — best part is picked
  faaa list               list sounds
  faaa share <id>         submit one of your sounds to the community catalog
  faaa theme [name]       list or set TUI theme
  faaa fail <id|off>      sound for rejected pushes
  faaa volume [0-100]     playback volume
  faaa on | off           unmute / mute (one push: FAAA_MUTE=1 git push)
  .faaa in a repo         line 1: repo's push sound, line 2: its fail sound
  faaa doctor             check setup
  faaa version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "faaa:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg := config.Load()
	if len(args) == 0 {
		return tui.Run(cfg, autoArm(&cfg))
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "play":
		return play(cfg, rest)
	case "_pushed": // from pre-push hook: <git pid> <remote> <url>, refs on stdin
		return pushed(rest)
	case "_await": // detached, in the repo root: <git pid> <checks...>
		if len(rest) == 0 {
			return nil
		}
		ok := hook.Await(rest[0], hook.Decode(rest[1:]))
		win, fail := repoSounds(cfg)
		if !ok {
			win = fail
		}
		if win == "" || !cfg.Enabled {
			return nil
		}
		return playSound(cfg, win, true)
	case "install":
		cfg.NoAutoArm = false
		return install(cfg, false)
	case "uninstall":
		if err := hook.Uninstall(cfg.PrevHooks); err != nil {
			return err
		}
		cfg.NoAutoArm = true // don't silently re-arm on next launch
		_ = config.Save(cfg)
		fmt.Println("✓ disarmed: hooks removed, git config restored")
		return nil
	case "update":
		ix, changed, err := catalog.Refresh(cfg.CatalogURL)
		if err != nil {
			return err
		}
		fmt.Printf("✓ %d sounds (updated %s)%s\n", len(ix.Sounds), ix.Updated.Format("Jan 2 15:04"),
			map[bool]string{true: "", false: " — already fresh"}[changed])
		return nil
	case "set":
		autoArm(&cfg)
		if len(rest) != 1 {
			return fmt.Errorf("usage: faaa set <id>")
		}
		s, ok := catalog.Find(rest[0])
		if !ok {
			return fmt.Errorf("no sound %q (see `faaa list`)", rest[0])
		}
		if err := fetch.Ensure(s, cfg.MaxSeconds); err != nil {
			return err
		}
		cfg.Sound = s.ID
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Println("✓ push sound →", s.Title)
		return nil
	case "add":
		return add(cfg, rest)
	case "search", "find":
		if len(rest) == 0 {
			return fmt.Errorf("usage: faaa search <words>")
		}
		res, err := search.Web(strings.Join(rest, " "))
		if err != nil {
			return err
		}
		for _, s := range res {
			fmt.Printf("  %-22s %-5s %s%s\n", s.ID, s.Source, s.Title, map[bool]string{true: " — " + s.Artist}[s.Artist != ""])
		}
		if len(res) > 0 {
			fmt.Println("\narm one: faaa set <id>")
		}
		return nil
	case "length", "len":
		if len(rest) == 0 {
			fmt.Printf("%gs\n", cfg.MaxSeconds)
			return nil
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(rest[0], "s"), 64)
		if err != nil || v < 1 || v > 60 {
			return fmt.Errorf("length must be 1–60 seconds")
		}
		cfg.MaxSeconds = v
		if err := config.Save(cfg); err != nil {
			return err
		}
		if s, ok := catalog.Find(cfg.Sound); ok {
			if err := fetch.Recut(s, v); err != nil {
				return err
			}
		}
		fmt.Printf("✓ clips are now up to %gs (armed sound re-cut)\n", v)
		return nil
	case "list", "ls":
		for _, s := range catalog.All(catalog.Cached()) {
			mark := " "
			if s.ID == cfg.Sound {
				mark = "✓"
			}
			fmt.Printf("%s %-28s %-10s %s\n", mark, s.ID, s.Source, s.Title)
		}
		return nil
	case "share":
		return share(rest)
	case "theme":
		if len(rest) == 0 {
			for _, n := range tui.ThemeNames() {
				mark := " "
				if n == cfg.Theme {
					mark = "✓"
				}
				fmt.Println(mark, n)
			}
			return nil
		}
		if _, ok := tui.Themes[rest[0]]; !ok {
			return fmt.Errorf("unknown theme %q", rest[0])
		}
		cfg.Theme = rest[0]
		return config.Save(cfg)
	case "fail":
		if len(rest) == 0 {
			fmt.Println(map[bool]string{true: "off", false: cfg.FailSound}[cfg.FailSound == ""])
			return nil
		}
		if rest[0] == "off" {
			cfg.FailSound = ""
			return config.Save(cfg)
		}
		s, ok := catalog.Find(rest[0])
		if !ok {
			return fmt.Errorf("no sound %q (see `faaa list`)", rest[0])
		}
		if err := fetch.Ensure(s, cfg.MaxSeconds); err != nil {
			return err
		}
		cfg.FailSound = s.ID
		fmt.Println("✓ rejected pushes now play", s.Title)
		return config.Save(cfg)
	case "volume", "vol":
		if len(rest) == 0 {
			v := cfg.Volume
			if v == 0 {
				v = 1
			}
			fmt.Printf("%.0f%%\n", v*100)
			return nil
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(rest[0], "%"), 64)
		if err != nil || v < 0 {
			return fmt.Errorf("usage: faaa volume 40   (percent)")
		}
		if v > 1 {
			v /= 100
		}
		cfg.Volume = min(v, 1)
		return config.Save(cfg)
	case "on", "off":
		cfg.Enabled = cmd == "on"
		return config.Save(cfg)
	case "doctor":
		return doctor(cfg)
	case "version", "--version", "-v":
		fmt.Println("faaa", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}
	fmt.Print(usage)
	return fmt.Errorf("unknown command %q", cmd)
}

// play is on the git hot path: no network, no catalog parse.
func play(cfg config.Config, rest []string) error {
	id := cfg.Sound
	if len(rest) > 0 {
		id = rest[0]
	} else if !cfg.Enabled {
		return nil
	}
	return playSound(cfg, id, false)
}

// repoSounds: a `.faaa` file in the repo root overrides the push sound for
// everyone on the team (line 1), and optionally the fail sound (line 2).
func repoSounds(cfg config.Config) (win, fail string) {
	win, fail = cfg.Sound, cfg.FailSound
	b, err := os.ReadFile(".faaa")
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if v := strings.TrimSpace(lines[0]); v != "" {
		win = v
	}
	if len(lines) > 1 {
		fail = strings.TrimSpace(lines[1])
	}
	return
}

// playSound plays id; fetch=true may download it first (only from the
// detached hook process, never on anything the user waits for).
func playSound(cfg config.Config, id string, fetchOK bool) error {
	if fetchOK && !catalog.Ready(id) {
		if s, ok := catalog.Find(id); ok {
			_ = fetch.Ensure(s, cfg.MaxSeconds)
		}
	}
	f := paths.Sound(id)
	if _, err := os.Stat(f); err != nil {
		if !audio.IsBuiltin(id) { // sound was deleted: fall back to default
			id = audio.DefaultID()
			f = paths.Sound(id)
		}
		if _, err := os.Stat(f); err != nil {
			if err := audio.WriteBuiltin(id, f, cfg.MaxSeconds); err != nil {
				return err
			}
		}
	}
	return audio.PlayDetached(cfg.Player, f, cfg.Volume)
}

// pushed runs inside pre-push: must be fast and never fail the push.
func pushed(args []string) error {
	if len(args) < 3 {
		return nil
	}
	if os.Getenv("FAAA_MUTE") != "" { // FAAA_MUTE=1 git push: silent this once
		return nil
	}
	checks, any := hook.ParsePrePush(os.Stdin, args[1], args[2])
	if !any {
		return nil // nothing to push
	}
	hook.Snapshot(checks)
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	// no checks = unverifiable (tag, refs/for/*, raw URL): plays optimistically
	_ = hook.Background(self, append([]string{"_await", args[0]}, hook.Encode(checks)...)...)
	return nil
}

// autoArm hooks git on first launch so pushes make noise with zero setup.
func autoArm(cfg *config.Config) string {
	if cfg.NoAutoArm || hook.Installed() {
		return ""
	}
	if err := install(*cfg, true); err != nil {
		return "○ couldn't arm git: " + err.Error()
	}
	*cfg = config.Load()
	return "◉ armed — every git push now plays your sound"
}

func install(cfg config.Config, quiet bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// Prefer the stable PATH entry (e.g. /opt/homebrew/bin/faaa) when it is
	// this same binary: versioned Cellar paths vanish on `brew upgrade`.
	// npm's PATH entry is a node wrapper (different file), so it's skipped.
	if lp, err := exec.LookPath("faaa"); err == nil {
		if a, err := filepath.Abs(lp); err == nil {
			ra, e1 := filepath.EvalSymlinks(a)
			rs, e2 := filepath.EvalSymlinks(self)
			if e1 == nil && e2 == nil && ra == rs {
				self = a
			}
		}
	}
	// hooks run under sh (git-bash on Windows): forward slashes are safe everywhere
	prev, mode, err := hook.Install(filepath.ToSlash(self), cfg.PrevHooks)
	if err != nil {
		return err
	}
	cfg.PrevHooks = prev
	if err := config.Save(cfg); err != nil {
		return err
	}
	if s, ok := catalog.Find(cfg.Sound); ok {
		_ = fetch.Ensure(s, cfg.MaxSeconds)
	}
	if quiet {
		return nil
	}
	fmt.Println("◉ armed — every successful git push, in every repo, now plays your sound")
	if mode == "hookspath" && prev != "" {
		fmt.Println("  your previous hooks at", prev, "still run")
	}
	fmt.Println("  run `faaa` to pick a sound")
	return nil
}

func add(cfg config.Config, rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("usage: faaa add <file | link> [name]")
	}
	if tools.IsLink(rest[0]) {
		fmt.Println("⇣ grabbing the sound…")
	}
	s, c, err := fetch.Add(rest[0], strings.Join(rest[1:], " "), cfg.MaxSeconds, func(m string) { fmt.Println("  " + m) })
	if err != nil {
		return err
	}
	cfg.Sound = s.ID
	if err := config.Save(cfg); err != nil {
		return err
	}
	autoArm(&cfg)
	msg := fmt.Sprintf("◉ armed %q (%.1fs)", s.Title, c.Len)
	if c.Full > c.Len+0.05 {
		msg += fmt.Sprintf(" — best part %s–%s of %s", clock(c.Start), clock(c.Start+c.Len), clock(c.Full))
	}
	fmt.Println(msg)
	return nil
}

func clock(sec float64) string {
	s := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

const repo = "https://github.com/OkeyAmy/faaa"

func share(rest []string) error {
	if len(rest) != 1 {
		return fmt.Errorf("usage: faaa share <id>")
	}
	var s catalog.Sound
	for _, x := range catalog.Mine() {
		if x.ID == rest[0] {
			s = x
		}
	}
	if s.ID == "" {
		return fmt.Errorf("%q is not one of your sounds (faaa add first)", rest[0])
	}
	body := fmt.Sprintf("### Sound\n- title: %s\n- source/link: %s\n- duration: %.1fs\n\n"+
		"Attach the audio file (mp3/wav, ≤15s) below.\n\n- [ ] I made this sound or have the right to share it.\n",
		s.Title, s.Page, s.Duration)
	link := repo + "/issues/new?labels=sound&title=" + url.QueryEscape("sound: "+s.Title) + "&body=" + url.QueryEscape(body)
	fmt.Println("opening:", link)
	return openURL(link)
}

func openURL(u string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	return c.Start()
}

func doctor(cfg config.Config) error {
	ok := func(b bool, msg, fix string) {
		if b {
			fmt.Println("✓", msg)
		} else {
			fmt.Println("✗", msg, "—", fix)
		}
	}
	p, err := audio.DetectPlayer()
	if len(cfg.Player) > 0 {
		p, err = cfg.Player, nil
	}
	ok(err == nil, fmt.Sprintf("audio player %v", p), fmt.Sprint(err))
	ok(hook.Installed(), "global hooks installed", "run `faaa install`")
	if t := hook.Target(); t != "" {
		_, err := os.Stat(filepath.FromSlash(t))
		ok(err == nil, "hooks call "+t, "binary moved; run `faaa install` again")
	}
	if o, err := hook.RepoOverride(); err == nil && hook.Mode() == "hookspath" {
		ok(o == "", "this repo uses global hooks", fmt.Sprintf(
			"repo sets core.hooksPath=%s (husky?). Upgrade git for config hooks, or add `faaa play &` to its pre-push", o))
		gd, _ := exec.Command("git", "rev-parse", "--git-common-dir").Output()
		for _, n := range hook.Unshimmed {
			if _, err := os.Stat(filepath.Join(strings.TrimSpace(string(gd)), "hooks", n)); err == nil {
				ok(false, "repo hook "+n, "not run while faaa uses core.hooksPath; upgrade git to switch to config hooks")
			}
		}
	}
	if m := hook.Mode(); m != "" {
		fmt.Println("  mode:", m)
	}
	ok(catalog.Ready(cfg.Sound), "sound ready: "+cfg.Sound, "run `faaa set "+cfg.Sound+"`")
	ok(cfg.Enabled, "push sounds on", "run `faaa on`")
	_, ff := exec.LookPath("ffmpeg")
	_, yt := exec.LookPath("yt-dlp")
	fmt.Printf("  optional: ffmpeg %v, yt-dlp %v\n", ff == nil, yt == nil)
	return nil
}
