package hook

import (
	"strings"
	"testing"
)

const sha = "1111111111111111111111111111111111111111"

func TestParsePrePushNamedRemote(t *testing.T) {
	in := "refs/heads/feat " + sha + " refs/heads/feat " + zero + "\n" +
		"refs/tags/v1 " + sha + " refs/tags/v1 " + zero + "\n"
	cs, pushed := ParsePrePush(strings.NewReader(in), "origin", "git@x:y.git")
	if !pushed || len(cs) != 1 {
		t.Fatalf("pushed=%v checks=%v", pushed, cs)
	}
	if cs[0].Tracking != "refs/remotes/origin/feat" || cs[0].Sha != sha {
		t.Fatalf("bad check %+v", cs[0])
	}
}

func TestParsePrePushRawURLUnverifiable(t *testing.T) {
	in := "refs/heads/main " + sha + " refs/heads/main " + zero + "\n"
	cs, pushed := ParsePrePush(strings.NewReader(in), "git@x:y.git", "git@x:y.git")
	if !pushed || len(cs) != 0 {
		t.Fatalf("raw URL push should be pushed but unverifiable: %v %v", pushed, cs)
	}
}

func TestParsePrePushNothing(t *testing.T) {
	if _, pushed := ParsePrePush(strings.NewReader(""), "origin", "u"); pushed {
		t.Fatal("empty stdin means nothing pushed")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []Check{{"refs/remotes/origin/a", sha, ""}, {"refs/remotes/origin/b", zero, sha}}
	out := Decode(Encode(in))
	if len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
		t.Fatalf("%v != %v", out, in)
	}
}

func TestConfigScriptExecsDirectly(t *testing.T) {
	s := Script("pre-push", "/opt/it's/faaa", "", false)
	if !strings.Contains(s, `exec '/opt/it'\''s/faaa' _pushed "$PPID" "$@"`) {
		t.Fatal(s)
	}
}

func TestCommandQuotesOnlyWhenNeeded(t *testing.T) {
	if got := command("/home/a/.config/faaa/hooks/pre-push"); strings.HasPrefix(got, "'") {
		t.Fatalf("plain path should not be quoted: %s", got)
	}
	if got := command("/home/John Doe/pre-push"); !strings.HasPrefix(got, "'") {
		t.Fatalf("path with space must be quoted: %s", got)
	}
}
