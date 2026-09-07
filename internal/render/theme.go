package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
)

// Theme holds every colour the diff renderer uses.
//
// Fields are grouped into semantic roles — surface, gutter, diff state, focus,
// annotation — rather than one flat palette, so a new preset only has to
// answer "what does a deleted line look like here?" instead of remembering
// which of a dozen greens meant what. The older field names (AddBg, Gutter,
// CursorBg, …) are kept as aliases because the TUI, the queue and the submit
// view already read them; presets fill both halves.
type Theme struct {
	// Name identifies the preset. Empty for a hand-built theme.
	Name string

	// Syntax is the chroma style name used for code highlighting.
	Syntax string

	// SyntaxMute blends syntax colours toward the surface, 0 (untouched) to 1
	// (invisible). Muting is what lets diff state win the page while code
	// keeps its shape. SyntaxMuteEmph is the weaker blend used for the tokens
	// worth keeping vivid: function, method, class and type names.
	SyntaxMute     float64
	SyntaxMuteEmph float64

	// Surface.
	Bg     string // the page behind unchanged code
	Fg     string // code with no syntax colour of its own
	Dim    string // secondary text: meta rows, ranges, hints
	Accent string // focus, hunk headers, keys

	// Gutter.
	GutterBg       string
	GutterSep      string // the rule between the old and new columns
	LineNumFg      string
	LineNumFocusFg string // the focused row's line number

	// Diff state. Edge is the left marker, Sign the +/− column, WordBg the
	// intra-line change shading, and BgFocus the tint a focused row keeps so
	// that focus never erases what kind of line it is.
	AddBg      string
	AddBgFocus string
	AddEdge    string
	AddSign    string
	AddWordBg  string

	DelBg      string
	DelBgFocus string
	DelEdge    string
	DelSign    string
	DelWordBg  string

	// MarkUnderline underlines intra-line changes as well as shading them,
	// for themes that cannot rely on the shading being perceived.
	MarkUnderline bool

	// Focus.
	CursorBg  string // focused context row
	CursorBar string // the focus bar in the edge column

	// Headers.
	FileBg string
	FileFg string
	HunkBg string
	HunkFg string
	MetaFg string

	// Annotations.
	NoteBg     string
	NoteFg     string // your own unsent notes
	NoteBodyFg string // annotation body text, which has to stay readable
	CommentFg  string // existing review comments from GitHub
	StaleFg    string // notes and comments that no longer anchor

	// File state.
	ReviewedFg string
	ChangedFg  string

	// Gutter is the compatibility alias for LineNumFg.
	Gutter string
	// AddFg and DelFg are the compatibility aliases for AddSign and DelSign.
	AddFg string
	DelFg string
}

// DefaultTheme is the dark preset, which is what crv shows when nothing has
// been configured.
func DefaultTheme() Theme { return darkTheme() }

// ThemeNames lists the built-in presets in a stable order.
func ThemeNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ThemeByName returns a built-in preset. The second result is false for a
// name crv does not know, which is how the config layer tells a theme name
// from a chroma style name.
func ThemeByName(name string) (Theme, bool) {
	build, ok := presets[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Theme{}, false
	}
	return build(), true
}

var presets = map[string]func() Theme{
	"dark":          darkTheme,
	"light":         lightTheme,
	"high-contrast": highContrastTheme,
}

// resolve fills the compatibility aliases from the semantic roles, so a preset
// states each colour once and nothing downstream reads an empty field.
func (t Theme) resolve() Theme {
	t.Gutter = t.LineNumFg
	t.AddFg = t.AddSign
	t.DelFg = t.DelSign
	return t
}

func darkTheme() Theme {
	return Theme{
		Name:           "dark",
		Syntax:         "catppuccin-mocha",
		SyntaxMute:     0.45,
		SyntaxMuteEmph: 0.15,

		Bg:     "#1c1f26",
		Fg:     "#c8ccd4",
		Dim:    "#7b8394",
		Accent: "#7aa2f7",

		GutterBg:       "#181b21",
		GutterSep:      "#3a4050",
		LineNumFg:      "#5c6370",
		LineNumFocusFg: "#e6e9ef",

		AddBg:      "#16261d",
		AddBgFocus: "#1e3728",
		AddEdge:    "#4f9d69",
		AddSign:    "#7fd88f",
		AddWordBg:  "#25603e",

		DelBg:      "#291a1e",
		DelBgFocus: "#3b242a",
		DelEdge:    "#b3596a",
		DelSign:    "#f07178",
		DelWordBg:  "#6e2733",

		CursorBg:  "#262b36",
		CursorBar: "#7aa2f7",

		FileBg: "#262b36",
		FileFg: "#e6e9ef",
		HunkBg: "#20242c",
		HunkFg: "#7aa2f7",
		MetaFg: "#7b8394",

		NoteBg:     "#22262f",
		NoteFg:     "#e5c07b",
		NoteBodyFg: "#c8ccd4",
		CommentFg:  "#56b6c2",
		StaleFg:    "#6b7280",

		ReviewedFg: "#7fd88f",
		ChangedFg:  "#e5c07b",
	}.resolve()
}

func lightTheme() Theme {
	return Theme{
		Name:           "light",
		Syntax:         "catppuccin-latte",
		SyntaxMute:     0.40,
		SyntaxMuteEmph: 0.10,

		Bg:     "#fbfbfa",
		Fg:     "#2b2f36",
		Dim:    "#6b7280",
		Accent: "#2f6fd0",

		GutterBg:       "#f1f1ef",
		GutterSep:      "#c9ccd2",
		LineNumFg:      "#9aa0aa",
		LineNumFocusFg: "#1f2329",

		AddBg:      "#e8f5ec",
		AddBgFocus: "#d3ebdc",
		AddEdge:    "#2f8a52",
		AddSign:    "#1f7a43",
		AddWordBg:  "#b3e0c3",

		DelBg:      "#fdecec",
		DelBgFocus: "#f7d9d9",
		DelEdge:    "#c04a4a",
		DelSign:    "#b02b2b",
		DelWordBg:  "#f4bebe",

		CursorBg:  "#e9ebf0",
		CursorBar: "#2f6fd0",

		FileBg: "#e5e7ec",
		FileFg: "#1f2329",
		HunkBg: "#f2f3f6",
		HunkFg: "#2f6fd0",
		MetaFg: "#6b7280",

		NoteBg:     "#f5f2e8",
		NoteFg:     "#8a6d1f",
		NoteBodyFg: "#3a3f46",
		CommentFg:  "#136f7a",
		StaleFg:    "#8b9099",

		ReviewedFg: "#1f7a43",
		ChangedFg:  "#8a6d1f",
	}.resolve()
}

func highContrastTheme() Theme {
	return Theme{
		Name:           "high-contrast",
		Syntax:         "github-dark",
		SyntaxMute:     0.12,
		SyntaxMuteEmph: 0,
		MarkUnderline:  true,

		Bg:     "#000000",
		Fg:     "#ffffff",
		Dim:    "#c0c0c0",
		Accent: "#7fd4ff",

		GutterBg:       "#0b0b0b",
		GutterSep:      "#767676",
		LineNumFg:      "#a8a8a8",
		LineNumFocusFg: "#ffffff",

		AddBg:      "#042b13",
		AddBgFocus: "#0a4620",
		AddEdge:    "#00d76a",
		AddSign:    "#3dff92",
		AddWordBg:  "#0a6b34",

		DelBg:      "#2b040b",
		DelBgFocus: "#460a15",
		DelEdge:    "#ff4d6d",
		DelSign:    "#ff8b99",
		DelWordBg:  "#7a0f22",

		CursorBg:  "#1c1c1c",
		CursorBar: "#ffd400",

		FileBg: "#1c1c1c",
		FileFg: "#ffffff",
		HunkBg: "#101010",
		HunkFg: "#7fd4ff",
		MetaFg: "#c0c0c0",

		NoteBg:     "#141414",
		NoteFg:     "#ffd400",
		NoteBodyFg: "#f2f2f2",
		CommentFg:  "#7fd4ff",
		StaleFg:    "#a8a8a8",

		ReviewedFg: "#3dff92",
		ChangedFg:  "#ffd400",
	}.resolve()
}

// mix blends fg toward bg by amount, 0 returning fg unchanged and 1 returning
// bg. Anything it cannot parse — a named colour, an ANSI index — is returned
// untouched rather than guessed at.
func mix(fg, bg string, amount float64) string {
	if amount <= 0 {
		return fg
	}
	if amount > 1 {
		amount = 1
	}
	fr, fg2, fb, ok := parseHex(fg)
	if !ok {
		return fg
	}
	br, bgc, bb, ok := parseHex(bg)
	if !ok {
		return fg
	}
	blend := func(a, b int) int {
		v := float64(a) + (float64(b)-float64(a))*amount
		return int(v + 0.5)
	}
	return fmt.Sprintf("#%02x%02x%02x", blend(fr, br), blend(fg2, bgc), blend(fb, bb))
}

func parseHex(s string) (r, g, b int, ok bool) {
	if !strings.HasPrefix(s, "#") {
		return 0, 0, 0, false
	}
	h := s[1:]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
}

// KnownSyntax reports whether chroma has a style by this name. It is how the
// config layer tells a mistyped theme name from a deliberate chroma style.
func KnownSyntax(name string) bool {
	return styles.Get(name) != nil && styles.Get(name).Name == name
}
