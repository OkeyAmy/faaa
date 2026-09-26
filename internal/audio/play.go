package audio

import (
	"errors"
	"os"
	"os/exec"
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

// Command builds the player command for file. FAAA_PLAYER overrides (tests).
func Command(player []string, file string) (*exec.Cmd, error) {
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
	args := append(append([]string{}, player[1:]...), arg)
	return exec.Command(player[0], args...), nil
}

// PlayDetached starts playback in its own session and returns immediately,
// so git push is never delayed.
func PlayDetached(player []string, file string) error {
	c, err := Command(player, file)
	if err != nil {
		return err
	}
	detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}
