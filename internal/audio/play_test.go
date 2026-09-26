package audio

import (
	"strings"
	"testing"
)

func TestPowerShellEmbedsQuotedPath(t *testing.T) {
	t.Setenv("FAAA_PLAYER", "")
	c, err := Command([]string{"powershell", "-NoProfile", "-Command"}, `C:\Users\O'Neil\a.wav`)
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
	c, _ := Command([]string{"aplay", "-q"}, "/x.wav")
	if got := strings.Join(c.Args, " "); got != "aplay -q /x.wav" {
		t.Fatal(got)
	}
}
