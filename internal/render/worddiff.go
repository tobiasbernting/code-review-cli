package render

import "unicode"

// span marks a byte range within a line that differs from its counterpart.
type span struct{ start, end int }

// maxWordDiffTokens caps the O(n*m) LCS below. Lines longer than this are
// almost always minified or generated, where word-level marking is noise.
const maxWordDiffTokens = 400

// wordDiff finds which parts of a deleted line and its paired added line
// actually changed, so the render can dim the unchanged remainder.
func wordDiff(oldText, newText string) (oldSpans, newSpans []span) {
	a := tokenize(oldText)
	b := tokenize(newText)
	if len(a) == 0 || len(b) == 0 || len(a) > maxWordDiffTokens || len(b) > maxWordDiffTokens {
		return nil, nil
	}

	// Trim the common prefix and suffix first; for a typical one-token edit
	// this reduces the LCS to a handful of cells.
	p := 0
	for p < len(a) && p < len(b) && a[p].text == b[p].text {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s].text == b[len(b)-1-s].text {
		s++
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	if len(ma) == 0 && len(mb) == 0 {
		return nil, nil
	}

	keepA, keepB := lcsKeep(ma, mb)
	oldSpans = spansFor(ma, keepA)
	newSpans = spansFor(mb, keepB)

	// If everything on a side changed, marking adds nothing over the row's own
	// background colour.
	if coversAll(oldSpans, oldText) && coversAll(newSpans, newText) {
		return nil, nil
	}
	return oldSpans, newSpans
}

type token struct {
	text  string
	start int
	end   int
}

// tokenize splits into identifier runs, whitespace runs, and single symbols,
// which is granular enough to isolate a renamed variable but coarse enough
// that the LCS stays cheap.
func tokenize(s string) []token {
	var out []token
	i := 0
	for i < len(s) {
		start := i
		switch c := rune(s[i]); {
		case isWordByte(s[i]):
			for i < len(s) && isWordByte(s[i]) {
				i++
			}
		case unicode.IsSpace(c):
			for i < len(s) && unicode.IsSpace(rune(s[i])) {
				i++
			}
		default:
			i++
		}
		out = append(out, token{text: s[start:i], start: start, end: i})
	}
	return out
}

func isWordByte(b byte) bool {
	return b == '_' || b >= 0x80 ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// lcsKeep returns, for each side, which tokens are part of the longest common
// subsequence (i.e. unchanged).
func lcsKeep(a, b []token) (keepA, keepB []bool) {
	n, m := len(a), len(b)
	keepA, keepB = make([]bool, n), make([]bool, m)
	if n == 0 || m == 0 {
		return
	}
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i].text == b[j].text {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i].text == b[j].text:
			keepA[i], keepB[j] = true, true
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}
	return
}

// spansFor merges the changed tokens into contiguous byte ranges.
func spansFor(toks []token, keep []bool) []span {
	var out []span
	for i, t := range toks {
		if keep[i] {
			continue
		}
		if n := len(out); n > 0 && out[n-1].end == t.start {
			out[n-1].end = t.end
			continue
		}
		out = append(out, span{t.start, t.end})
	}
	return out
}

func coversAll(spans []span, text string) bool {
	trimmedStart := 0
	for trimmedStart < len(text) && text[trimmedStart] == ' ' {
		trimmedStart++
	}
	return len(spans) == 1 && spans[0].start <= trimmedStart && spans[0].end >= len(text)
}
