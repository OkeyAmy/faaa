package upgrade

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		exe  string
		want string
	}{
		{"/usr/local/bin/faaa", "direct"},
		{"/home/me/.local/bin/faaa", "direct"},
		{"/home/me/.nvm/versions/node/v20.11.0/lib/node_modules/@okeyamy/faaa/bin/faaa", "npm"},
		{`C:\Users\me\AppData\Roaming\npm\node_modules\@okeyamy\faaa\bin\faaa.exe`, "npm"},
		{"/opt/homebrew/Cellar/faaa/0.0.7/bin/faaa", "brew"},
		{"/usr/local/Homebrew/bin/faaa", "brew"},
		{"/home/linuxbrew/.linuxbrew/bin/faaa", "brew"},
	}
	for _, c := range cases {
		if got := Detect(c.exe); got != c.want {
			t.Errorf("Detect(%q) = %q, want %q", c.exe, got, c.want)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.0.7", "0.0.7", 0},
		{"v0.0.7", "0.0.7", 0},
		{"0.0.6", "0.0.7", -1},
		{"0.0.7", "0.0.6", 1},
		{"0.0.10", "0.0.9", 1},  // numeric, not string, compare
		{"0.0.9", "0.0.10", -1}, // the other way round
		{"v1.0.0", "v0.9.9", 1},
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-rc1", "1.2.3-rc1", 0},
		{"dev", "0.0.7", -1}, // an unresolved dev build compares as 0.0.0
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestHint(t *testing.T) {
	cases := []struct {
		method string
		want   string
	}{
		{"direct", "curl -fsSL https://raw.githubusercontent.com/OkeyAmy/faaa/main/install.sh | sh"},
		{"npm", "npm i -g @okeyamy/faaa@latest"},
		{"brew", "brew upgrade faaa"},
	}
	for _, c := range cases {
		if got := Hint(c.method); got != c.want {
			t.Errorf("Hint(%q) = %q, want %q", c.method, got, c.want)
		}
	}
	// Detect only returns the three methods above; an unknown method must
	// still produce a usable command (the install script).
	if got := Hint("unknown"); !strings.Contains(got, "install.sh") {
		t.Errorf("Hint(unknown) = %q, want the install script", got)
	}
}
