package main

import "testing"

func TestTitleizeBreadcrumbLabel(t *testing.T) {
	cases := map[string]string{
		"ai connections": "Ai Connections",
		"don't":          "Don't",
		"what's new":     "What's New",
		"":               "",
		"   ":            "",
		"already Done":   "Already Done",
		"ünïcode path":   "Ünïcode Path",
		"d'angelo":       "D'angelo",
	}
	for in, want := range cases {
		if got := titleizeBreadcrumbLabel(in); got != want {
			t.Errorf("titleizeBreadcrumbLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
