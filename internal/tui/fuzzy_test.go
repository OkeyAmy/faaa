package tui

import "testing"

func TestFuzzy(t *testing.T) {
	if fuzzy("vbm", "vine boom") < 0 {
		t.Fatal("subsequence should match")
	}
	if fuzzy("xyz", "vine boom") != -1 {
		t.Fatal("non-match should be -1")
	}
	if fuzzy("boom", "vine boom") <= fuzzy("boom", "b o o m") {
		t.Fatal("consecutive match should score higher")
	}
}

func TestMix(t *testing.T) {
	if got := mix("#000000", "#FFFFFF", 0); got != "#000000" {
		t.Fatal(got)
	}
	if got := mix("#000000", "#FFFFFF", 1); got != "#FFFFFF" {
		t.Fatal(got)
	}
}
