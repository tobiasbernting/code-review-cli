package render

// Theme holds every colour the diff renderer uses. Defaults target a dark
// terminal background; --theme and the config file will override it later.
type Theme struct {
	Syntax string // chroma style name

	AddBg     string
	DelBg     string
	AddWordBg string
	DelWordBg string

	AddFg  string
	DelFg  string
	Gutter string

	FileBg   string
	FileFg   string
	HunkBg   string
	HunkFg   string
	MetaFg   string
	CursorBg string

	NoteFg     string // your own unsent notes
	CommentFg  string // existing review comments from GitHub
	StaleFg    string // notes and comments that no longer anchor
	NoteBg     string
	ReviewedFg string
	ChangedFg  string
}

func DefaultTheme() Theme {
	return Theme{
		Syntax:    "catppuccin-mocha",
		AddBg:     "#12291d",
		DelBg:     "#33191b",
		AddWordBg: "#1f5136",
		DelWordBg: "#6e2733",
		AddFg:     "#7fd88f",
		DelFg:     "#f07178",
		Gutter:    "#5c6370",
		FileBg:    "#2c313a",
		FileFg:    "#e6e6e6",
		HunkBg:    "#21252b",
		HunkFg:    "#61afef",
		MetaFg:    "#7f848e",
		CursorBg:  "#3a3f4b",

		NoteFg:     "#e5c07b",
		CommentFg:  "#56b6c2",
		StaleFg:    "#7f848e",
		NoteBg:     "#252a33",
		ReviewedFg: "#7fd88f",
		ChangedFg:  "#e5c07b",
	}
}
