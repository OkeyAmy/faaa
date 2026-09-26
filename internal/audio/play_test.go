package audio

import (
	"strings"
	"testing"
)

func TestPowerShellEmbedsQuotedPath(t *testing.T) {
	t.Setenv("FAAA_PLAYER", "")
	c, err := Command([]string{"powershell", "-NoProfile", "-Command"}, `C:\Users\O'Neil\a.wav`, 1)
	if err != nil {
		t.Fatal(err)
	}
	last := c.Args[len(c.Args)-1]
	if !strings.Contains(last, `'C:\Users\O''Neil\a.wav'`) || !strings.HasSuffix(last, ".PlaySync()") {
		t.Fatalf("bad script: %s", last)
	}
}

func TestUnixPlayerGetsPathArg(t *testing.T) {
	t.Setenv("FAAA_PLAYER", "")
	c, _ := Command([]string{"aplay", "-q"}, "/x.wav", 1)
	if got := strings.Join(c.Args, " "); got != "aplay -q /x.wav" {
		t.Fatal(got)
	}
}

func TestVolumeFlags(t *testing.T) {
	t.Setenv("FAAA_PLAYER", "")
	c, _ := Command([]string{"/usr/bin/pw-play"}, "/x.wav", 0.5)
	if got := strings.Join(c.Args, " "); got != "/usr/bin/pw-play --volume=0.50 /x.wav" {
		t.Fatal(got)
	}
	c, _ = Command([]string{"afplay"}, "/x.wav", 0.25)
	if got := strings.Join(c.Args, " "); got != "afplay -v 0.25 /x.wav" {
		t.Fatal(got)
	}
}
