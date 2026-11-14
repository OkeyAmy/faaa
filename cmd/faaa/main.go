// faaa: play a fun sound every time you git push.
package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
	"github.com/okeyamy/faaa/internal/upgrade"
)

var version = "dev"

const usage = `faaa — git push, but louder

usage:
  faaa                    open the sound picker
  faaa install            arm git (automatic on first launch)
  faaa uninstall          disarm, restore previous git config
  faaa update             refresh the sound list now (auto every launch)
  faaa upgrade            check for a newer release (and how to install it)
  faaa play [id|row|name] play current (or given) sound
  faaa set <id|row|name>  choose push sound
  faaa add <file|link> [name] your own sound: a file, or a link to any
                          TikTok (no setup) / Reels / Shorts / YouTube post
  faaa search <words>     find any sound online
  faaa length [sec]       clip length (default 15, max 60) — best part is picked
  faaa list               list sounds
  faaa share <id>         submit one of your sounds to the community catalog
  faaa theme [name]       list or set TUI theme
  faaa fail <id|row|name|off>  sound for rejected pushes
  faaa volume [1-100]     playback gain (not system volume); faaa off mutes
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

// interactive reports whether the picker has somewhere to draw. Bubble Tea
// opens /dev/tty itself when stdin is redirected, so probe the same device and
// fail with a readable message instead of a raw syscall error. Windows keeps
// its own console handling untouched.
func interactive() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func run(args []string) error {
	cfg := config.Load()
	if len(args) == 0 {
		if !interactive() {
			return errors.New("the sound picker needs a terminal — run `faaa` from your terminal, or try `faaa list`")
		}
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
			// The raw error is Go's transport detail ("dial tcp ..."): say
			// what failed and what still works instead.
			return fmt.Errorf("couldn't refresh the sound list from %s — check the URL and your network (the built-in list still works)", cfg.CatalogURL)
		}
		fmt.Printf("✓ %d sounds (updated %s)%s\n", len(ix.Sounds), ix.Updated.Format("Jan 2 15:04"),
			map[bool]string{true: "", false: " — already fresh"}[changed])
		return nil
	case "upgrade":
		return upgradeCmd(rest)
	case "set":
		autoArm(&cfg)
		if len(rest) == 0 {
			return fmt.Errorf("usage: faaa set <id|row|name>")
		}
		q := strings.Join(rest, " ")
		s, err := resolveSound(q)
		if err != nil {
			return err
		}
		if err := fetch.Ensure(s, cfg.MaxSeconds); err != nil {
			return err
		}
		cfg.Sound = s.ID
		if err := config.Save(cfg); err != nil {
			return err
		}
		// Echo the title (and the row, when a number was typed) so a listing
		// overwritten by another terminal is visible immediately.
		row := ""
		if n, err := strconv.Atoi(q); err == nil {
			row = fmt.Sprintf(" (row %d)", n)
		}
		fmt.Println("✓ push sound →", s.Title+row)
		return nil
	case "add":
		return add(cfg, rest)
	case "search", "find":
		if len(rest) == 0 {
			return fmt.Errorf("usage: faaa search <words>")
		}
		q := strings.Join(rest, " ")
		if len([]rune(strings.TrimSpace(q))) < minQuery {
			fmt.Printf("faaa: type at least %d characters\n", minQuery)
			return nil
		}
		res, err := search.Web(q)
		if err != nil {
			return err
		}
		if len(res) == 0 {
			fmt.Printf("faaa: no results for %q\n", q)
			return nil
		}
		if len(res) > shownResults {
			res = res[:shownResults]
		}
		printListing(res)
		if err := catalog.SaveListing(res); err != nil {
			return err
		}
		pick := 2
		if len(res) < pick {
			pick = len(res)
		}
		words := strings.Fields(displayTitle(res[pick-1].Title))
		if len(words) > 3 {
			words = words[:3]
		}
		example := strings.Join(words, " ")
		fmt.Printf("\n  arm it:     faaa set %d\n", pick)
		if example != "" && !strings.ContainsAny(example, "\"'$`\\") {
			fmt.Printf("  or by name: faaa set %s   (any words from the title, any order)\n", example)
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
		for _, s := range listSounds(cfg.Sound) {
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
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Println("✓ theme →", rest[0])
		return nil
	case "fail":
		if len(rest) == 0 {
			fmt.Println(map[bool]string{true: "off", false: cfg.FailSound}[cfg.FailSound == ""])
			return nil
		}
		q := strings.Join(rest, " ")
		if q == "off" {
			cfg.FailSound = ""
			return config.Save(cfg)
		}
		s, err := resolveSound(q)
		if err != nil {
			return err
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
			fmt.Println("  faaa's own playback gain — your system/device volume is untouched (`faaa off` mutes)")
			return nil
		}
		v, err := strconv.ParseFloat(strings.TrimSuffix(rest[0], "%"), 64)
		if err != nil || v < 1 || v > 100 {
			return fmt.Errorf("volume must be 1-100 (percent) — to mute run `faaa off`")
		}
		cfg.Volume = v / 100
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("✓ volume → %.0f%%\n", v)
		return nil
	case "on", "off":
		cfg.Enabled = cmd == "on"
		return config.Save(cfg)
	case "doctor":
		return doctor(cfg)
	case "version", "--version", "-v":
		if len(rest) > 0 {
			return errors.New("usage: faaa version")
		}
		fmt.Println("faaa", version)
		return nil
	case "help", "--help", "-h":
		if len(rest) > 0 {
			return fmt.Errorf("unknown help topic %q — run `faaa help`", rest[0])
		}
		fmt.Print(usage)
		return nil
	}
	fmt.Print(usage)
	return fmt.Errorf("unknown command %q", cmd)
}

// upgradeCmd is `faaa upgrade`: a read-only notice. It never downloads or
// replaces anything — it names the installer that ships the next release.
func upgradeCmd(rest []string) error {
	if len(rest) > 0 {
		return errors.New("usage: faaa upgrade")
	}
	self, err := os.Executable()
	if err != nil {
		return errors.New("couldn't find the running faaa binary — reinstall from " + repo)
	}
	cur := currentVersion()
	latest, newer, err := upgrade.Check(cur)
	if err != nil {
		return err
	}
	state := "already up to date"
	if newer {
		state = "update available"
	}
	fmt.Printf("faaa %s — latest %s (%s)\n", cur, latest, state)
	fmt.Printf("  install: %s\n", upgrade.Hint(upgrade.Detect(self)))
	return nil
}

// currentVersion shows the ldflag version, or the module version the Go
// toolchain embeds when `go install` left the ldflag at "dev".
func currentVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return version
}

// play with an explicit id is user-invoked, so a bad id is an error instead of
// a silent fallback. No args keeps the old path (and stays cheap for hooks).
func play(cfg config.Config, rest []string) error {
	if len(rest) > 0 {
		q := strings.Join(rest, " ")
		s, err := resolveSound(q)
		if err != nil {
			if !catalog.Ready(q) { // sound already on disk but not indexed
				return err
			}
			return playSound(cfg, q, false)
		}
		// Playback needs a local file, and search results are only metadata
		// until something fetches them. `play` is an explicit request, so fetch
		// here instead of silently falling back to the default sound.
		if !catalog.Ready(s.ID) {
			fmt.Fprintf(os.Stderr, "⇣ downloading %s…\n", displayTitle(s.Title))
			if err := fetch.Ensure(s, cfg.MaxSeconds); err != nil {
				return fmt.Errorf("couldn't download %q: %w", displayTitle(s.Title), err)
			}
		}
		return playSound(cfg, s.ID, false)
	}
	if !cfg.Enabled {
		return nil
	}
	return playSound(cfg, cfg.Sound, false)
}

// resolveSound maps typed input to a sound: exact id, row number from the last
// search, unique id prefix, or unique title fragment. Ambiguous input prints up
// to five candidates and changes nothing.
func resolveSound(q string) (catalog.Sound, error) {
	s, cands, ok := catalog.Resolve(q)
	if ok {
		return s, nil
	}
	if len(cands) > 0 {
		fmt.Printf("faaa: %q matches %d sounds — be more specific:\n", q, len(cands))
		last := catalog.Listing()
		for _, c := range cands {
			row := 0
			for i, x := range last {
				if x.ID == c.ID {
					row = i + 1
					break
				}
			}
			if row > 0 {
				fmt.Printf("  • %s   (row %d)\n", displayTitle(c.Title), row)
			} else {
				fmt.Printf("  • %s   %s\n", displayTitle(c.Title), c.ID)
			}
		}
		return catalog.Sound{}, fmt.Errorf("ambiguous sound %q — nothing changed", q)
	}
	return catalog.Sound{}, fmt.Errorf("no sound %q — try `faaa search %s` or `faaa list`", q, q)
}

// listSounds is the catalog plus the last search listing and the armed sound,
// so `faaa list` marks a sound that was picked by number and only exists in the
// live-search cache.
func listSounds(armed string) []catalog.Sound {
	all := catalog.All(catalog.Cached())
	seen := map[string]bool{}
	for _, s := range all {
		seen[s.ID] = true
	}
	add := func(list []catalog.Sound) {
		for _, s := range list {
			if !seen[s.ID] {
				seen[s.ID] = true
				all = append(all, s)
			}
		}
	}
	add(catalog.Listing())
	if s, ok := catalog.Find(armed); ok && !seen[s.ID] {
		all = append(all, s)
	}
	return all
}

// Search listings are numbered so the user never has to type an id:
// `faaa set 2` / `faaa play 2` refer back to the last listing.
const (
	minQuery     = 2  // shorter queries match everything on Myinstants
	shownResults = 12 // enough to pick from, short enough to read
)

// printListing numbers res in display order and lines the title column up on
// the longest title actually present (ids vary from 4 to 40+ characters).
func printListing(res []catalog.Sound) {
	titles := make([]string, len(res))
	width := 0
	for i, s := range res {
		titles[i] = displayTitle(s.Title)
		if n := len([]rune(titles[i])); n > width {
			width = n
		}
	}
	if width > 40 {
		width = 40
	}
	if width < 1 {
		width = 1
	}
	for i, s := range res {
		line := fmt.Sprintf("%3d  %s", i+1, padRight(fitTitle(titles[i], width), width))
		line = strings.TrimRight(line, " ")
		if d := durationCell(s.Duration); d != "" {
			line += "  " + d
		}
		fmt.Println(line)
	}
}

// displayTitle drops Myinstants' redundant "meme" qualifier ("vine boom sound
// meme"); the source column that used to carry it is gone.
func displayTitle(s string) string {
	words := strings.Fields(s)
	kept := words[:0]
	for _, w := range words {
		if !strings.EqualFold(w, "meme") {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return s
	}
	return strings.Join(kept, " ")
}

func padRight(s string, w int) string {
	if n := len([]rune(s)); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func fitTitle(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w < 2 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func durationCell(d float64) string {
	if d <= 0 { // live search hits have no duration until they are downloaded
		return ""
	}
	return clock(d)
}

// knownSound reports whether id is a builtin, a sound already on disk, or an
// id from the catalog, recent searches, or the user's own list. Local only.
func knownSound(id string) bool {
	if id == "" {
		return false
	}
	if audio.IsBuiltin(id) || catalog.Ready(id) {
		return true
	}
	_, ok := catalog.Find(id)
	return ok
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

// warnRepoSounds prints a note when `.faaa` names a sound we cannot resolve.
// Playback still falls back to the default; this must never fail a push.
// The file is read only when it exists and nothing here touches the network.
func warnRepoSounds() {
	b, err := os.ReadFile(".faaa")
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		id := strings.TrimSpace(line)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if knownSound(id) {
			continue
		}
		fmt.Fprintf(os.Stderr, "faaa: .faaa names unknown sound %q — using %s instead (see `faaa list`)\n", id, audio.DefaultID())
	}
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
	warnRepoSounds()
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
	existing := map[string]bool{}
	for _, m := range catalog.Mine() {
		existing[m.ID] = true
	}
	s, c, err := fetch.Add(rest[0], strings.Join(rest[1:], " "), cfg.MaxSeconds, func(m string) { fmt.Println("  " + m) })
	if err != nil {
		return addError(rest[0], err)
	}
	cfg.Sound = s.ID
	if err := config.Save(cfg); err != nil {
		return err
	}
	autoArm(&cfg)
	if existing[s.ID] {
		fmt.Printf("✓ %q already exists — replaced your previous copy\n", s.Title)
	}
	msg := fmt.Sprintf("◉ armed %q (%.1fs)", s.Title, c.Len)
	if c.Full > c.Len+0.05 {
		msg += fmt.Sprintf(" — best part %s–%s of %s", clock(c.Start), clock(c.Start+c.Len), clock(c.Full))
	}
	fmt.Println(msg)
	return nil
}

// addError rewords decode failures. The decoder's raw error names a temp file
// and an ffmpeg exit code, which means nothing to someone who just picked the
// wrong file; the pipeline itself is unchanged.
func addError(src string, err error) error {
	if !errors.Is(err, audio.ErrNotAudio) {
		return err
	}
	if tools.IsLink(src) {
		return errors.New("couldn't find audio at that link — try a direct mp3/wav URL, or save the file and run `faaa add ./sound.mp3`")
	}
	return errors.New("that file isn't audio this tool can read — try mp3, wav, m4a, or ogg (faaa add ./sound.mp3)")
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

// playerName mirrors the executable names audio.Command knows how to pass a
// volume flag to.
func playerName(player []string) string {
	if len(player) == 0 {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(player[0]), ".exe")
}

func playerHasGain(player []string) bool {
	switch playerName(player) {
	case "pw-play", "pw-cat", "paplay", "afplay", "ffplay", "mpv":
		return true
	}
	return false
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
	if err == nil && !playerHasGain(p) {
		fmt.Printf("  ⚠ %s cannot apply gain — `faaa volume` will not change loudness (install pw-play or paplay)\n", playerName(p))
	}
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
	// Builtins ship inside the binary and are decoded on first play, so a
	// missing WAV is not a problem for them — only for downloaded sounds.
	ok(catalog.Ready(cfg.Sound) || audio.IsBuiltin(cfg.Sound), "sound ready: "+cfg.Sound, "run `faaa set "+cfg.Sound+"`")
	ok(cfg.Enabled, "push sounds on", "run `faaa on`")
	_, ff := exec.LookPath("ffmpeg")
	_, yt := exec.LookPath("yt-dlp")
	fmt.Printf("  optional: ffmpeg %v, yt-dlp %v\n", ff == nil, yt == nil)
	return nil
}
