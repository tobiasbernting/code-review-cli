package gitsrc

import "testing"

func TestRangeEnd(t *testing.T) {
	for spec, want := range map[string]string{
		"main...feature": "feature",
		"HEAD~3..HEAD":   "HEAD",
		"main..":         "HEAD",
		"main...":        "HEAD",
		"main":           "", // a single revision is diffed against the working tree
		"HEAD~3":         "",
	} {
		if got := rangeEnd(spec); got != want {
			t.Errorf("rangeEnd(%q) = %q, want %q", spec, got, want)
		}
	}
}
