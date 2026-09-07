// Package render turns parsed diffs into styled terminal rows.
//
// It knows nothing about bubbletea: the TUI is a viewport over Rows, and the
// non-interactive path prints the same Rows straight to stdout. Keeping the
// renderer TUI-free is what makes golden-file tests possible.
//
// # Row anatomy
//
// Every row is drawn on the same grid, so the eye can lock onto one column and
// scan down it:
//
//	▎  12 │  14 + func Greet(name string) string {
//	│   │     │  │ │
//	│   │     │  │ └─ code, syntax muted so diff state wins
//	│   │     │  └─── sign: + added, − deleted, blank unchanged
//	│   │     └────── new-side line number
//	│   └──────────── the rule separating old from new
//	└──────────────── edge: diff marker, or the focus bar on the cursor row
//
// Add and delete are stated three times over — edge marker, sign, row tint —
// so the diff still reads with colour disabled or unperceived, and so that
// focusing a row can lift its tone without erasing what kind of line it is.
package render

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
)

const (
	tabWidth = 4
	// noteHang is how far the continuation lines of an annotation hang in
	// from its first line, so a wrapped body reads as one paragraph rather
	// than as several notes.
	noteHang = 2
)

// The glyphs that carry meaning without colour.
const (
	edgeChange   = "▎" // an added or deleted line
	edgeFocus    = "┃" // the cursor row
	edgeNote     = "▌" // an annotation
	gutterRule   = "│"
	signAdd      = "+"
	signDel      = "−"
	overflowMark = "›" // the line continues past the right edge
	markerDone   = "✓"
	markerAged   = "~"
)

type RowKind int

const (
	RowFile RowKind = iota
	RowMeta         // rename/binary/mode notes under a file header
	RowHunk
	RowCode
	RowNote // a local note or an existing review comment
	RowSection
	RowSpacer
)

// Density is how much breathing room the document gets. It buys hierarchy —
// hunks and annotation groups get separated from the code around them — rather
// than blank lines everywhere, which would cost more screen than they explain.
type Density int

const (
	DensityComfortable Density = iota
	DensityCompact
)

// ParseDensity resolves a configured density name.
func ParseDensity(s string) (Density, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "comfortable":
		return DensityComfortable, true
	case "compact":
		return DensityCompact, true
	}
	return DensityComfortable, false
}

func (d Density) String() string {
	if d == DensityCompact {
		return "compact"
	}
	return "comfortable"
}

// Layout is the structural half of presentation: what the document is shaped
// like, as opposed to what colour it is.
type Layout struct {
	Density Density
}

// Row is one visual line. Rows carry their origin (file, hunk) so navigation
// and, later, note anchoring can work directly off the rendered document.
type Row struct {
	Kind    RowKind
	FileIdx int
	HunkIdx int
	Line    diffparse.Line
	Segs    []Segment
	Marks   []span // byte ranges that differ from the paired line
	Text    string // header text, for non-code rows

	// Detail is the secondary half of a header — a file's +/− counts, a
	// hunk's line range — drawn quietly on the right so the name it belongs
	// to keeps the left edge to itself.
	Detail string

	// Annotation rows carry the note or comment they render, and Cont marks
	// the second and later lines of a wrapped body.
	Ann  *Annotation
	Cont bool

	// Reviewed and Changed decorate a file header.
	Reviewed bool
	Changed  bool
}

// Document is the full rendered diff plus the index needed to jump around it.
type Document struct {
	Files     []*diffparse.FileDiff
	Rows      []Row
	FileRows  []int // Rows index of each file header
	HunkRows  []int // Rows index of every hunk header, in order
	Layout    Layout
	gutterOld int
	gutterNew int
}

// Build renders every file, drawing the overlay's notes and comments beneath
// the lines they belong to. Pass the zero Overlay for a plain diff, and the
// zero Layout for the default comfortable density.
func Build(files []*diffparse.FileDiff, h *Highlighter, ov Overlay, layout Layout) *Document {
	d := &Document{Files: files, Layout: layout, gutterOld: 3, gutterNew: 3}
	roomy := layout.Density == DensityComfortable

	for fi, f := range files {
		reviewed, changed := ov.fileState(f.Path())
		d.FileRows = append(d.FileRows, len(d.Rows))
		d.Rows = append(d.Rows, Row{
			Kind: RowFile, FileIdx: fi, HunkIdx: -1,
			Text: f.Path(), Detail: fileStats(f),
			Reviewed: reviewed, Changed: changed,
		})

		for _, m := range metaNotes(f) {
			d.Rows = append(d.Rows, Row{Kind: RowMeta, FileIdx: fi, HunkIdx: -1, Text: m})
		}
		// Detached annotations go directly under the header: they belong to
		// this file but no longer to any line in it.
		for _, group := range ov.detached(f.Path()) {
			d.Rows = append(d.Rows, Row{
				Kind: RowSection, FileIdx: fi, HunkIdx: -1, Text: group.Title,
			})
			for _, a := range group.Items {
				d.Rows = append(d.Rows, annotationRow(fi, -1, a))
			}
		}
		if f.IsBinary {
			d.Rows = append(d.Rows, Row{Kind: RowSpacer, FileIdx: fi, HunkIdx: -1})
			continue
		}

		for hi, hunk := range f.Hunks() {
			// A rule between hunks, but not between a file header and the
			// hunk it introduces: they belong together.
			if roomy && hi > 0 {
				d.Rows = append(d.Rows, Row{Kind: RowSpacer, FileIdx: fi, HunkIdx: hi})
			}
			d.HunkRows = append(d.HunkRows, len(d.Rows))
			d.Rows = append(d.Rows, Row{
				Kind: RowHunk, FileIdx: fi, HunkIdx: hi,
				Text: hunk.Section, Detail: hunkRangeText(hunk),
			})
			segs := highlightHunk(h, f, hunk)
			marks := markHunk(hunk)
			for li, ln := range hunk.Lines {
				d.trackGutter(ln)
				d.Rows = append(d.Rows, Row{
					Kind: RowCode, FileIdx: fi, HunkIdx: hi,
					Line: ln, Segs: segs[li], Marks: marks[li],
				})
				// Annotations hang off the new-side line number, which is the
				// coordinate GitHub review comments use.
				anns := ov.at(f.Path(), ln.NewNum)
				for _, a := range anns {
					d.Rows = append(d.Rows, annotationRow(fi, hi, a))
				}
				// Give a group of annotations its own air, so the code line
				// after it does not read as part of the conversation.
				if roomy && len(anns) > 0 && li < len(hunk.Lines)-1 {
					d.Rows = append(d.Rows, Row{Kind: RowSpacer, FileIdx: fi, HunkIdx: hi})
				}
			}
		}
		d.Rows = append(d.Rows, Row{Kind: RowSpacer, FileIdx: fi, HunkIdx: -1})
	}
	// Annotations whose file is not in this diff at all still have to be
	// seen, so they land in their own section after everything else.
	for _, group := range ov.orphaned() {
		d.Rows = append(d.Rows, Row{
			Kind: RowSection, FileIdx: len(files), HunkIdx: -1, Text: group.Title,
		})
		for _, a := range group.Items {
			d.Rows = append(d.Rows, annotationRow(len(files), -1, a))
		}
	}
	return d
}

// annotationRow keeps the body unwrapped: wrapping happens at paint time,
// because the terminal can be resized after the document is built.
func annotationRow(fileIdx, hunkIdx int, a Annotation) Row {
	ann := a
	return Row{Kind: RowNote, FileIdx: fileIdx, HunkIdx: hunkIdx, Ann: &ann}
}

func (d *Document) trackGutter(ln diffparse.Line) {
	if w := digits(ln.OldNum); w > d.gutterOld {
		d.gutterOld = w
	}
	if w := digits(ln.NewNum); w > d.gutterNew {
		d.gutterNew = w
	}
}

// GutterWidth is the width of everything left of the code: the edge marker,
// both line-number columns with the rule between them, and the sign.
func (d *Document) GutterWidth() int {
	// edge + " " + old + " │ " + new + " " + sign + " "
	return 2 + d.gutterOld + 3 + d.gutterNew + 3
}

// highlightHunk syntax-highlights the old and new sides of a hunk as whole
// documents, then redistributes the segments back onto individual lines.
func highlightHunk(h *Highlighter, f *diffparse.FileDiff, hunk diffparse.Hunk) [][]Segment {
	var oldIdx, newIdx []int
	var oldSrc, newSrc []string
	for i, ln := range hunk.Lines {
		if ln.Kind != diffparse.KindAdd {
			oldIdx = append(oldIdx, i)
			oldSrc = append(oldSrc, ln.Text)
		}
		if ln.Kind != diffparse.KindDel {
			newIdx = append(newIdx, i)
			newSrc = append(newSrc, ln.Text)
		}
	}
	out := make([][]Segment, len(hunk.Lines))
	assign := func(idx []int, src []string, path string) {
		if len(idx) == 0 {
			return
		}
		lines := h.Lines(path, strings.Join(src, "\n"))
		for i, row := range idx {
			if i < len(lines) {
				out[row] = lines[i]
			}
		}
	}
	// Context lines get the new side's colouring; it is written last and wins.
	assign(oldIdx, oldSrc, f.OldPath)
	assign(newIdx, newSrc, f.NewPath)
	return out
}

// markHunk pairs each run of deleted lines with the run of added lines that
// follows it, and word-diffs them pairwise. Runs of unequal length are left
// unmarked: the pairing would be a guess, and a wrong guess highlights the
// wrong tokens, which is worse than highlighting none.
func markHunk(hunk diffparse.Hunk) [][]span {
	marks := make([][]span, len(hunk.Lines))
	i := 0
	for i < len(hunk.Lines) {
		if hunk.Lines[i].Kind != diffparse.KindDel {
			i++
			continue
		}
		delStart := i
		for i < len(hunk.Lines) && hunk.Lines[i].Kind == diffparse.KindDel {
			i++
		}
		addStart := i
		for i < len(hunk.Lines) && hunk.Lines[i].Kind == diffparse.KindAdd {
			i++
		}
		delN, addN := addStart-delStart, i-addStart
		if delN != addN || delN == 0 {
			continue
		}
		for k := 0; k < delN; k++ {
			d, a := delStart+k, addStart+k
			marks[d], marks[a] = wordDiff(hunk.Lines[d].Text, hunk.Lines[a].Text)
		}
	}
	return marks
}

func fileStats(f *diffparse.FileDiff) string {
	if f.Additions == 0 && f.Deletions == 0 {
		return ""
	}
	return fmt.Sprintf("+%d %s%d", f.Additions, signDel, f.Deletions)
}

func metaNotes(f *diffparse.FileDiff) []string {
	var out []string
	switch f.Status {
	case diffparse.Renamed:
		out = append(out, "renamed from "+f.OldPath)
	case diffparse.Copied:
		out = append(out, "copied from "+f.OldPath)
	case diffparse.Added:
		out = append(out, "new file")
	case diffparse.Deleted:
		out = append(out, "deleted")
	}
	if f.OldMode != "" && f.NewMode != "" && f.OldMode != f.NewMode {
		out = append(out, "mode "+f.OldMode+" → "+f.NewMode)
	}
	if f.IsBinary {
		out = append(out, "binary file — not shown")
	}
	return out
}

// hunkRangeText is the @@ range, kept as the header's quiet half: it matters
// when you are cross-referencing with git, and never when you are reading.
func hunkRangeText(h diffparse.Hunk) string {
	return fmt.Sprintf("-%d,%d +%d,%d", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
}

// Renderer paints Rows. Styles are cached because a full-screen repaint builds
// one per (foreground, background) pair on every visible row.
type Renderer struct {
	Theme Theme
	Doc   *Document

	mu     sync.Mutex
	styles map[styleKey]lipgloss.Style
	muted  map[[2]string]string
}

type styleKey struct {
	fg, bg    string
	bold      bool
	underline bool
}

func NewRenderer(theme Theme, doc *Document) *Renderer {
	return &Renderer{
		Theme:  theme,
		Doc:    doc,
		styles: map[styleKey]lipgloss.Style{},
		muted:  map[[2]string]string{},
	}
}

func (r *Renderer) style(fg, bg string) lipgloss.Style {
	return r.styleKey(styleKey{fg: fg, bg: bg})
}

func (r *Renderer) bold(fg, bg string) lipgloss.Style {
	return r.styleKey(styleKey{fg: fg, bg: bg, bold: true})
}

func (r *Renderer) styleKey(key styleKey) lipgloss.Style {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.styles[key]; ok {
		return s
	}
	s := lipgloss.NewStyle()
	if key.fg != "" {
		s = s.Foreground(lipgloss.Color(key.fg))
	}
	if key.bg != "" {
		s = s.Background(lipgloss.Color(key.bg))
	}
	if key.bold {
		s = s.Bold(true)
	}
	if key.underline {
		s = s.Underline(true)
	}
	r.styles[key] = s
	return s
}

// syntaxFg mutes one syntax colour toward the surface. Names keep most of
// their colour; everything else recedes, which is what stops a rainbow of
// tokens from competing with the add/delete tint for attention.
func (r *Renderer) syntaxFg(fg string, emph bool) string {
	t := r.Theme
	if fg == "" {
		return t.Fg
	}
	amount := t.SyntaxMute
	if emph {
		amount = t.SyntaxMuteEmph
	}
	if amount <= 0 {
		return fg
	}
	key := [2]string{fg, fmt.Sprint(emph)}
	r.mu.Lock()
	if m, ok := r.muted[key]; ok {
		r.mu.Unlock()
		return m
	}
	r.mu.Unlock()

	m := mix(fg, t.Bg, amount)
	r.mu.Lock()
	r.muted[key] = m
	r.mu.Unlock()
	return m
}

// rowTones is everything the row's diff state and focus decide between them.
type rowTones struct {
	bg      string // behind the code
	gutter  string // behind the line numbers
	edge    string // the edge glyph
	edgeFg  string
	sign    string
	signFg  string
	wordBg  string
	numFg   string
	numBold bool
}

func (r *Renderer) tones(kind diffparse.LineKind, focus bool) rowTones {
	t := r.Theme
	tn := rowTones{
		bg: t.Bg, gutter: t.GutterBg, edge: " ", sign: " ",
		signFg: t.LineNumFg, numFg: t.LineNumFg,
	}
	switch kind {
	case diffparse.KindAdd:
		tn.bg, tn.wordBg = t.AddBg, t.AddWordBg
		tn.edge, tn.edgeFg = edgeChange, t.AddEdge
		tn.sign, tn.signFg = signAdd, t.AddSign
	case diffparse.KindDel:
		tn.bg, tn.wordBg = t.DelBg, t.DelWordBg
		tn.edge, tn.edgeFg = edgeChange, t.DelEdge
		tn.sign, tn.signFg = signDel, t.DelSign
	}
	if !focus {
		return tn
	}
	// Focus lifts the tone by one step and takes over the edge column and the
	// new-side number. The sign, the tint and the syntax all survive it, so a
	// focused deletion still reads as a deletion.
	switch kind {
	case diffparse.KindAdd:
		tn.bg = t.AddBgFocus
	case diffparse.KindDel:
		tn.bg = t.DelBgFocus
	default:
		tn.bg = t.CursorBg
	}
	tn.gutter = tn.bg
	tn.edge, tn.edgeFg = edgeFocus, t.CursorBar
	tn.numFg, tn.numBold = t.LineNumFocusFg, true
	return tn
}

// Render paints one row clipped to width, scrolled horizontally by hoffset.
func (r *Renderer) Render(row Row, width, hoffset int, cursor bool) string {
	if width <= 0 {
		return ""
	}
	switch row.Kind {
	case RowFile:
		return r.fileRow(row, width)
	case RowNote:
		return r.noteRow(row, width, cursor)
	case RowSection:
		return r.sectionRow(row, width)
	case RowMeta:
		return r.metaRow(row, width)
	case RowHunk:
		return r.hunkRow(row, width)
	case RowSpacer:
		return ""
	}
	return r.codeRow(row, width, hoffset, cursor)
}

func (r *Renderer) codeRow(row Row, width, hoffset int, cursor bool) string {
	t := r.Theme
	tn := r.tones(row.Line.Kind, cursor)

	var b strings.Builder
	b.WriteString(r.style(tn.edgeFg, tn.gutter).Render(tn.edge))
	b.WriteString(r.style(t.LineNumFg, tn.gutter).Render(" " + num(row.Line.OldNum, r.Doc.gutterOld)))
	b.WriteString(r.style(t.GutterSep, tn.gutter).Render(" " + gutterRule + " "))
	b.WriteString(r.styleKey(styleKey{fg: tn.numFg, bg: tn.gutter, bold: tn.numBold}).
		Render(num(row.Line.NewNum, r.Doc.gutterNew)))
	b.WriteString(r.style(t.GutterSep, tn.gutter).Render(" "))
	b.WriteString(r.bold(tn.signFg, tn.bg).Render(tn.sign))
	b.WriteString(r.style("", tn.bg).Render(" "))

	codeWidth := width - r.Doc.GutterWidth()
	b.WriteString(r.code(row, codeWidth, hoffset, tn))
	return b.String()
}

// code slices the line into display cells so horizontal scrolling, tab
// expansion and wide runes all behave, then coalesces neighbouring cells that
// share styling back into as few escape sequences as possible.
func (r *Renderer) code(row Row, width, hoffset int, tn rowTones) string {
	if width <= 0 {
		return ""
	}
	cells := buildCells(row.Segs, row.Marks)

	// A line that runs past the edge says so, so a truncated line is never
	// mistaken for a short one. The marker costs the last column.
	total := 0
	for _, c := range cells {
		total += c.w
	}
	overflow := total > hoffset+width && width > 1
	if overflow {
		width--
	}

	var b strings.Builder
	col, used := 0, 0
	var runKey styleKey
	var run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		b.WriteString(r.styleKey(runKey).Render(run.String()))
		run.Reset()
	}
	keyFor := func(c cell) styleKey {
		key := styleKey{fg: r.syntaxFg(c.fg, c.emph), bg: tn.bg}
		if c.mark && tn.wordBg != "" {
			key.bg = tn.wordBg
			key.underline = r.Theme.MarkUnderline
		}
		return key
	}
	for _, c := range cells {
		if col+c.w <= hoffset {
			col += c.w
			continue
		}
		if used+c.w > width {
			break
		}
		key := keyFor(c)
		if run.Len() > 0 && key != runKey {
			flush()
		}
		runKey = key
		run.WriteRune(c.r)
		col += c.w
		used += c.w
	}
	flush()
	if used < width {
		b.WriteString(r.style("", tn.bg).Render(strings.Repeat(" ", width-used)))
	}
	if overflow {
		b.WriteString(r.style(r.Theme.Dim, tn.bg).Render(overflowMark))
	}
	return b.String()
}

type cell struct {
	r    rune
	w    int
	fg   string
	mark bool
	emph bool
}

func buildCells(segs []Segment, marks []span) []cell {
	var cells []cell
	byteOff, col := 0, 0
	for _, s := range segs {
		for _, ru := range s.Text {
			marked := inSpans(marks, byteOff)
			if ru == '\t' {
				n := tabWidth - col%tabWidth
				for i := 0; i < n; i++ {
					cells = append(cells, cell{r: ' ', w: 1, fg: s.Fg, mark: marked, emph: s.Emph})
				}
				col += n
				byteOff++
				continue
			}
			w := runewidth.RuneWidth(ru)
			if w == 0 {
				w = 1
			}
			cells = append(cells, cell{r: ru, w: w, fg: s.Fg, mark: marked, emph: s.Emph})
			col += w
			byteOff += len(string(ru))
		}
	}
	return cells
}

func inSpans(spans []span, off int) bool {
	for _, s := range spans {
		if off >= s.start && off < s.end {
			return true
		}
	}
	return false
}

// fileRow draws the header as a full-width band, prefixed with a review
// marker. A file that changed after being marked keeps its tick and gains a
// tilde: silently unticking would hide that you had already read it.
func (r *Renderer) fileRow(row Row, width int) string {
	t := r.Theme
	marker, markerFg := " ", t.FileFg
	switch {
	case row.Reviewed && row.Changed:
		marker, markerFg = markerAged, t.ChangedFg
	case row.Reviewed:
		marker, markerFg = markerDone, t.ReviewedFg
	}

	detail := row.Detail
	if row.Reviewed && row.Changed {
		detail = "changed since reviewed  " + detail
	}
	return r.band(" "+marker+" ", row.Text, detail, width, t.FileFg, t.Dim, t.FileBg, markerFg)
}

// hunkRow labels the gutter columns it sits above, so the two number columns
// are named where a reader first meets them, and carries the section the hunk
// falls in. The @@ range trails on the right, quietly.
func (r *Renderer) hunkRow(row Row, width int) string {
	t := r.Theme
	var b strings.Builder
	b.WriteString(r.style(t.GutterSep, t.HunkBg).Render("  "))
	b.WriteString(r.style(t.Dim, t.HunkBg).Render(label("old", r.Doc.gutterOld)))
	b.WriteString(r.style(t.GutterSep, t.HunkBg).Render(" " + gutterRule + " "))
	b.WriteString(r.style(t.Dim, t.HunkBg).Render(label("new", r.Doc.gutterNew)))
	b.WriteString(r.style("", t.HunkBg).Render("  "))

	head := lipgloss.Width(b.String())
	text := row.Text
	rest := width - head
	if rest <= 0 {
		return r.pad(b.String(), width, t.HunkBg)
	}
	detail := row.Detail
	if lipgloss.Width(text)+lipgloss.Width(detail)+3 > rest {
		detail = ""
	}
	gap := rest - lipgloss.Width(detail) - 1
	b.WriteString(r.style(t.HunkFg, t.HunkBg).Render(clip(text, maxi(gap, 0))))
	if detail != "" {
		pad := gap - lipgloss.Width(clip(text, maxi(gap, 0)))
		b.WriteString(r.style("", t.HunkBg).Render(strings.Repeat(" ", maxi(pad, 0))))
		b.WriteString(r.style(t.Dim, t.HunkBg).Render(detail + " "))
	}
	return r.pad(b.String(), width, t.HunkBg)
}

func (r *Renderer) metaRow(row Row, width int) string {
	t := r.Theme
	// Meta rows belong to the file header above them, so they indent to its
	// title rather than to the code column.
	indent := strings.Repeat(" ", mini(3, width))
	line := r.style("", t.Bg).Render(indent) +
		r.style(t.MetaFg, t.Bg).Render(clip(row.Text, width-len(indent)))
	return r.pad(line, width, t.Bg)
}

// noteRow draws the compact, one-line form used away from the cursor.
func (r *Renderer) noteRow(row Row, width int, cursor bool) string {
	lines := r.RenderLines(row, width, 0, cursor, 1)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// RenderLines paints a row as one or more terminal lines. Only annotations
// expand; maxLines <= 0 means no limit and is used by plain output.
//
// An annotation gets a coloured edge marker, a muted panel and body text that
// stays as readable as the code above it — one you have to squint at is one
// you skip.
func (r *Renderer) RenderLines(row Row, width, hoffset int, cursor bool, maxLines int) []string {
	if row.Kind != RowNote {
		return []string{r.Render(row, width, hoffset, cursor)}
	}
	t := r.Theme
	a := row.Ann
	if a == nil {
		return nil
	}

	fg := t.NoteFg
	switch {
	case a.NeedsReanchor || a.Outdated || a.Resolved:
		fg = t.StaleFg
	case a.Kind == AnnComment || a.Kind == AnnThread:
		fg = t.CommentFg
	}

	bg, edgeFg := t.NoteBg, fg
	if cursor {
		bg, edgeFg = t.CursorBg, t.CursorBar
	}

	indent := r.Doc.GutterWidth()
	// Continuation lines hang in from the first, so the block reads as one
	// paragraph; the wrap has to leave room for that.
	contentWidth := width - indent - noteHang
	if contentWidth < 1 {
		return []string{r.pad("", width, bg)}
	}
	label, body := splitLabel(annotationText(a, maxLines == 1))
	wrapped := WrapText(label+body, contentWidth)
	if maxLines > 0 && len(wrapped) > maxLines {
		wrapped = wrapped[:maxLines]
		if contentWidth == 1 {
			wrapped[maxLines-1] = "…"
		} else {
			wrapped[maxLines-1] = runewidth.Truncate(wrapped[maxLines-1], contentWidth-1, "") + "…"
		}
	}

	bodyFg := t.NoteBodyFg
	if a.NeedsReanchor || a.Outdated || a.Resolved {
		bodyFg = t.StaleFg
	}

	lines := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		var b strings.Builder
		b.WriteString(r.style(edgeFg, bg).Render(edgeNote))
		pad := indent - 1
		if i > 0 {
			pad += noteHang
		}
		b.WriteString(r.style("", bg).Render(strings.Repeat(" ", pad)))
		// Who is speaking stays in the annotation's own colour and weight;
		// what they said takes the body colour, so it reads like prose.
		head := ""
		if i == 0 && strings.HasPrefix(line, label) {
			head = label
			line = strings.TrimPrefix(line, label)
		}
		if head != "" {
			b.WriteString(r.bold(fg, bg).Render(head))
		}
		b.WriteString(r.style(bodyFg, bg).Render(clip(line, contentWidth-lipgloss.Width(head))))
		lines = append(lines, r.pad(b.String(), width, bg))
	}
	return lines
}

// splitLabel divides an annotation's first line into who is speaking and what
// they said. Everything up to and including the first ": " is the label —
// author, state badges, line range — which is exactly what annotationText
// builds.
func splitLabel(text string) (label, body string) {
	if i := strings.Index(text, ": "); i >= 0 {
		return text[:i+2], text[i+2:]
	}
	return "", text
}

// annotationText is everything an annotation says on one line before it is
// wrapped: who is speaking, what state the discussion is in, and the body.
func annotationText(a *Annotation, compact bool) string {
	if a.Kind == AnnThread {
		marker := "− "
		if a.Collapsed {
			marker = "+ "
		}
		count := a.ReplyCount + 1
		label := fmt.Sprintf("thread · %d comment%s", count, pluralWord(count))
		if a.Outdated {
			label += " [outdated]"
		}
		if !a.ResolutionKnown {
			label += " [resolution unavailable]"
		} else if a.Resolved {
			label += " [resolved]"
		} else {
			label += " [unresolved]"
		}
		if a.New {
			label += " [new]"
		}
		if a.Updated {
			label += " [updated]"
		}
		if a.Collapsed && a.Body != "" {
			label += ": " + a.Author + ": " + flatten(a.Body)
		}
		return marker + label
	}

	label := "you"
	if a.Author != "" {
		label = a.Author
	}
	if a.NeedsReanchor {
		label += " [needs re-anchor]"
	}
	if a.Kind == AnnComment {
		if a.Outdated {
			label += " [outdated]"
		}
		if !a.ResolutionKnown {
			label += " [resolution unavailable]"
		} else if a.Resolved {
			label += " [resolved]"
		}
	}
	if a.New {
		label += " [new]"
	}
	if a.Updated {
		label += " [updated]"
	}
	if a.StartLine > 0 && a.StartLine != a.Line {
		label += fmt.Sprintf(" L%d-%d", a.StartLine, a.Line)
	}
	body := a.Body
	if compact {
		body = flatten(body)
	}
	return label + ": " + body
}

func flatten(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func pluralWord(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// WrapText wraps without flattening explicit newlines or indentation.
func WrapText(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		wrapped := runewidth.Wrap(line, width)
		out = append(out, strings.Split(wrapped, "\n")...)
	}
	return out
}

// sectionRow heads a group of annotations that no longer belong to a line —
// or to any file in this diff. It is a heading, so it is drawn like one.
func (r *Renderer) sectionRow(row Row, width int) string {
	t := r.Theme
	indent := strings.Repeat(" ", mini(3, width))
	line := r.style("", t.Bg).Render(indent) +
		r.bold(t.Dim, t.Bg).Render(clip(row.Text, width-len(indent)))
	return r.pad(line, width, t.Bg)
}

// band draws a full-width header: a marker, a title, and a detail pushed to
// the right edge. It is what makes a file header read as a heading rather than
// as another line of the diff.
func (r *Renderer) band(marker, title, detail string, width int, fg, detailFg, bg, markerFg string) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(r.bold(markerFg, bg).Render(marker))
	room := width - lipgloss.Width(marker)
	if lipgloss.Width(detail)+2 > room {
		detail = ""
	}
	gap := room - lipgloss.Width(detail) - 1
	shown := clip(title, maxi(gap, 0))
	b.WriteString(r.bold(fg, bg).Render(shown))
	if detail != "" {
		pad := gap - lipgloss.Width(shown)
		b.WriteString(r.style("", bg).Render(strings.Repeat(" ", maxi(pad, 0))))
		b.WriteString(r.style(detailFg, bg).Render(detail + " "))
	}
	return r.pad(b.String(), width, bg)
}

func (r *Renderer) pad(s string, width int, bg string) string {
	if w := lipgloss.Width(s); w < width {
		return s + r.style("", bg).Render(strings.Repeat(" ", width-w))
	}
	return s
}

func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(s, width, "…")
}

func num(n, w int) string {
	if n == 0 {
		return strings.Repeat(" ", w)
	}
	return fmt.Sprintf("%*d", w, n)
}

// label right-aligns a gutter column's name, matching the numbers below it.
func label(s string, w int) string {
	if len(s) > w {
		return s[:w]
	}
	return fmt.Sprintf("%*s", w, s)
}

func digits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
