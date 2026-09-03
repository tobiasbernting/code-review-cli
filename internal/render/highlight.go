package render

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Segment is a run of text sharing one foreground colour. Background is left
// to the diff renderer, which owns add/delete/word-change shading.
type Segment struct {
	Text string
	Fg   string // "#rrggbb", or "" for the terminal default
}

// Highlighter tokenises source with chroma. It highlights a whole hunk side at
// once rather than line by line, so multi-line constructs (block comments,
// raw strings) colour correctly.
type Highlighter struct {
	style   *chroma.Style
	enabled bool

	mu     sync.Mutex
	lexers map[string]chroma.Lexer

	// cache memoises highlighting per (path, source). The document is rebuilt
	// whenever a note is added or a file is marked reviewed, and re-lexing a
	// large pull request on every keystroke is the difference between instant
	// and unusable.
	cache map[string][][]Segment
}

func NewHighlighter(styleName string, enabled bool) *Highlighter {
	st := styles.Get(styleName)
	if st == nil {
		st = styles.Fallback
	}
	return &Highlighter{
		style:   st,
		enabled: enabled,
		lexers:  map[string]chroma.Lexer{},
		cache:   map[string][][]Segment{},
	}
}

// Lines highlights source and returns one segment slice per line. The result
// always has exactly as many entries as source has lines.
func (h *Highlighter) Lines(path, source string) [][]Segment {
	plain := splitPlain(source)
	if !h.enabled {
		return plain
	}

	key := path + "\x00" + source
	h.mu.Lock()
	cached, ok := h.cache[key]
	h.mu.Unlock()
	if ok {
		return cached
	}

	it, err := h.lexerFor(path).Tokenise(nil, source)
	if err != nil {
		return plain
	}

	out := [][]Segment{{}}
	for tok := it(); tok != chroma.EOF; tok = it() {
		fg := ""
		if e := h.style.Get(tok.Type); e.Colour.IsSet() {
			fg = e.Colour.String()
		}
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				out = append(out, []Segment{})
			}
			if p != "" {
				last := len(out) - 1
				out[last] = append(out[last], Segment{Text: p, Fg: fg})
			}
		}
	}
	// Tokenise appends a trailing newline to input that lacks one; drop the
	// empty line it produces so the count matches the source.
	if len(out) > len(plain) {
		out = out[:len(plain)]
	}
	for len(out) < len(plain) {
		out = append(out, []Segment{})
	}

	h.mu.Lock()
	h.cache[key] = out
	h.mu.Unlock()
	return out
}

func (h *Highlighter) lexerFor(path string) chroma.Lexer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l, ok := h.lexers[path]; ok {
		return l
	}
	l := lexers.Match(path)
	if l == nil {
		l = lexers.Fallback
	}
	l = chroma.Coalesce(l)
	h.lexers[path] = l
	return l
}

func splitPlain(source string) [][]Segment {
	lines := strings.Split(source, "\n")
	out := make([][]Segment, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = []Segment{}
			continue
		}
		out[i] = []Segment{{Text: l}}
	}
	return out
}
