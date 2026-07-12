package main

import (
	"strings"
	"testing"
)

func TestColonCommandHint(t *testing.T) {
	cases := []struct {
		text       string
		wantEmpty  bool
		wantSubstr string
	}{
		{"", true, ""},
		{"ls -la", true, ""},
		{":open", false, "open shell"},
		{":close", false, "close open shell"},
		{":upgrade", false, "latest release"},
		{":upgrade v0.4.0", false, "v0.4.0"},
		{":restart ", false, "<service>"},
		{":restart nginx", false, "nginx"},
		{":o", false, "typing"},
		{":res", false, "typing"},
		{":bogus", false, "unknown command"},
	}
	for _, c := range cases {
		got := colonCommandHint(c.text)
		if c.wantEmpty {
			if got != "" {
				t.Errorf("colonCommandHint(%q) = %q, want empty", c.text, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantSubstr) {
			t.Errorf("colonCommandHint(%q) = %q, want substring %q", c.text, got, c.wantSubstr)
		}
	}
}

func TestColonCommandGhostSuffix(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"", ""},
		{"ls -la", ""},
		{":", ""}, // nothing typed after ':' yet — no suggestion
		{":o", "pen"},
		{":op", "en"},
		{":open", ""},  // already complete
		{":open ", ""}, // args started
		{":c", "lose"},
		{":r", "estart"},
		{":restart ", ""}, // args started
		{":u", "pgrade"},
		{":bogus", ""},
	}
	for _, c := range cases {
		got := colonCommandGhostSuffix(c.text)
		if got != c.want {
			t.Errorf("colonCommandGhostSuffix(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
