package render

import "testing"

func slice(s string, sp span) string { return s[sp.start:sp.end] }

func TestWordDiffIsolatesChangedToken(t *testing.T) {
	oldT := "	count := len(items)"
	newT := "	total := len(items)"
	o, n := wordDiff(oldT, newT)
	if len(o) != 1 || len(n) != 1 {
		t.Fatalf("got %d/%d spans, want 1/1", len(o), len(n))
	}
	if got := slice(oldT, o[0]); got != "count" {
		t.Errorf("old span = %q, want count", got)
	}
	if got := slice(newT, n[0]); got != "total" {
		t.Errorf("new span = %q, want total", got)
	}
}

func TestWordDiffMarksInsertionOnly(t *testing.T) {
	oldT := "foo(a)"
	newT := "foo(a, b)"
	o, n := wordDiff(oldT, newT)
	if len(o) != 0 {
		t.Errorf("old side gained spans %v, want none", o)
	}
	if len(n) != 1 || slice(newT, n[0]) != ", b" {
		t.Errorf("new spans = %v (%q), want a single \", b\"", n, slice(newT, n[0]))
	}
}

// A rewritten line should produce no marks: the row's own background already
// says the whole line changed, and marking all of it is pure noise.
func TestWordDiffSkipsFullRewrite(t *testing.T) {
	o, n := wordDiff("aaa", "zzz")
	if o != nil || n != nil {
		t.Errorf("got %v/%v, want no spans for a full rewrite", o, n)
	}
}

func TestWordDiffSkipsOversizedLines(t *testing.T) {
	long := ""
	for i := 0; i < maxWordDiffTokens+10; i++ {
		long += "x "
	}
	if o, n := wordDiff(long, long+"y"); o != nil || n != nil {
		t.Error("expected oversized lines to be skipped")
	}
}

func TestTokenizeOffsetsAreAbsolute(t *testing.T) {
	toks := tokenize("a + bb")
	want := []string{"a", " ", "+", " ", "bb"}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(want))
	}
	for i, w := range want {
		if toks[i].text != w {
			t.Errorf("token %d = %q, want %q", i, toks[i].text, w)
		}
		if got := "a + bb"[toks[i].start:toks[i].end]; got != w {
			t.Errorf("token %d offsets select %q, want %q", i, got, w)
		}
	}
}
