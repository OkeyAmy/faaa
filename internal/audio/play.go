package audio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// candidates in preference order per OS. All accept a WAV path as last arg.
func candidates() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"afplay"}}
	case "windows":
		return [][]string{{"powershell", "-NoProfile", "-NonInteractive", "-Command"}}
	default:
		return [][]string{{"pw-play"}, {"paplay"}, {"aplay", "-q"}, {"ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet"}}
	}
}

// DetectPlayer returns the first available player command (without file arg).
func DetectPlayer() ([]string, error) {
	for _, c := range candidates() {
		if p, err := exec.LookPath(c[0]); err == nil {
			return append([]string{p}, c[1:]...), nil
		}
	}
	return nil, errors.New("no audio player found (need pw-play, paplay, aplay, afplay or ffplay)")
}

// Command builds the player command for file at volume vol (0..1; players
// without a volume flag play at full level).
func Command(player []string, file string, vol float64) (*exec.Cmd, error) {
	if o := os.Getenv("FAAA_PLAYER"); o != "" {
		player = []string{o}
	}
	if len(player) == 0 {
		var err error
		if player, err = DetectPlayer(); err != nil {
			return nil, err
		}
	}
	arg := file
	if last := player[len(player)-1]; strings.EqualFold(last, "-Command") {
		// PowerShell joins trailing args into the script text, so $args
		// doesn't work: embed the path as a quoted literal instead.
		arg = "(New-Object Media.SoundPlayer '" + strings.ReplaceAll(file, "'", "''") + "').PlaySync()"
	}
	args := append([]string{}, player[1:]...)
	if vol > 0 && vol < 1 {
		switch strings.TrimSuffix(filepath.Base(player[0]), ".exe") {
		case "pw-play", "pw-cat":
			args = append(args, fmt.Sprintf("--volume=%.2f", vol))
		case "paplay":
			args = append(args, fmt.Sprintf("--volume=%d", int(vol*65536)))
		case "afplay":
			args = append(args, "-v", fmt.Sprintf("%.2f", vol))
		case "ffplay":
			args = append(args, "-volume", fmt.Sprint(int(vol*100)))
		case "mpv":
			args = append(args, fmt.Sprintf("--volume=%d", int(vol*100)))
		}
	}
	args = append(args, arg)
	return exec.Command(player[0], args...), nil
}

// PlayDetached starts playback in its own session and returns immediately,
// so git push is never delayed.
func PlayDetached(player []string, file string, vol float64) error {
	c, err := Command(player, file, vol)
	if err != nil {
		return err
	}
	detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}
