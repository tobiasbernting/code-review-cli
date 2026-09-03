// Package render turns parsed diffs into styled terminal rows.
//
// It knows nothing about bubbletea: the TUI is a viewport over Rows, and the
// non-interactive path prints the same Rows straight to stdout. Keeping the
// renderer TUI-free is what makes golden-file tests possible.
package render

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
)

const tabWidth = 4

type RowKind int

const (
	RowFile RowKind = iota
	RowMeta         // rename/binary/mode notes under a file header
	RowHunk
	RowCode
	RowSpacer
)

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
}

// Document is the full rendered diff plus the index needed to jump around it.
type Document struct {
	Files     []*diffparse.FileDiff
	Rows      []Row
	FileRows  []int // Rows index of each file header
	HunkRows  []int // Rows index of every hunk header, in order
	gutterOld int
	gutterNew int
}

// Build renders every file. Hunk parsing happens here, so callers that only
// need the file list should not call Build.
func Build(files []*diffparse.FileDiff, h *Highlighter) *Document {
	d := &Document{Files: files, gutterOld: 3, gutterNew: 3}

	for fi, f := range files {
		d.FileRows = append(d.FileRows, len(d.Rows))
		d.Rows = append(d.Rows, Row{Kind: RowFile, FileIdx: fi, HunkIdx: -1, Text: fileHeaderText(f)})

		for _, m := range metaNotes(f) {
			d.Rows = append(d.Rows, Row{Kind: RowMeta, FileIdx: fi, HunkIdx: -1, Text: m})
		}
		if f.IsBinary {
			d.Rows = append(d.Rows, Row{Kind: RowSpacer, FileIdx: fi, HunkIdx: -1})
			continue
		}

		for hi, hunk := range f.Hunks() {
			d.HunkRows = append(d.HunkRows, len(d.Rows))
			d.Rows = append(d.Rows, Row{
				Kind: RowHunk, FileIdx: fi, HunkIdx: hi,
				Text: hunkHeaderText(hunk),
			})
			segs := highlightHunk(h, f, hunk)
			marks := markHunk(hunk)
			for li, ln := range hunk.Lines {
				d.trackGutter(ln)
				d.Rows = append(d.Rows, Row{
					Kind: RowCode, FileIdx: fi, HunkIdx: hi,
					Line: ln, Segs: segs[li], Marks: marks[li],
				})
			}
		}
		d.Rows = append(d.Rows, Row{Kind: RowSpacer, FileIdx: fi, HunkIdx: -1})
	}
	return d
}

func (d *Document) trackGutter(ln diffparse.Line) {
	if w := digits(ln.OldNum); w > d.gutterOld {
		d.gutterOld = w
	}
	if w := digits(ln.NewNum); w > d.gutterNew {
		d.gutterNew = w
	}
}

// GutterWidth is the total width of the line-number columns and separator.
func (d *Document) GutterWidth() int { return d.gutterOld + d.gutterNew + 3 }

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

func fileHeaderText(f *diffparse.FileDiff) string {
	stats := ""
	if f.Additions > 0 || f.Deletions > 0 {
		stats = fmt.Sprintf("  +%d −%d", f.Additions, f.Deletions)
	}
	return f.Path() + stats
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

func hunkHeaderText(h diffparse.Hunk) string {
	s := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Section != "" {
		s += " " + h.Section
	}
	return s
}

// Renderer paints Rows. Styles are cached because a full-screen repaint builds
// one per (foreground, background) pair on every visible row.
type Renderer struct {
	Theme Theme
	Doc   *Document

	mu     sync.Mutex
	styles map[[2]string]lipgloss.Style
}

func NewRenderer(theme Theme, doc *Document) *Renderer {
	return &Renderer{Theme: theme, Doc: doc, styles: map[[2]string]lipgloss.Style{}}
}

func (r *Renderer) style(fg, bg string) lipgloss.Style {
	key := [2]string{fg, bg}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.styles[key]; ok {
		return s
	}
	s := lipgloss.NewStyle()
	if fg != "" {
		s = s.Foreground(lipgloss.Color(fg))
	}
	if bg != "" {
		s = s.Background(lipgloss.Color(bg))
	}
	r.styles[key] = s
	return s
}

// Render paints one row clipped to width, scrolled horizontally by hoffset.
func (r *Renderer) Render(row Row, width, hoffset int, cursor bool) string {
	if width <= 0 {
		return ""
	}
	t := r.Theme
	switch row.Kind {
	case RowFile:
		return r.pad(r.style(t.FileFg, t.FileBg).Bold(true).Render(clip(" "+row.Text, width)), width, t.FileBg)
	case RowMeta:
		return r.pad(r.style(t.MetaFg, "").Render(clip("   "+row.Text, width)), width, "")
	case RowHunk:
		return r.pad(r.style(t.HunkFg, t.HunkBg).Render(clip(" "+row.Text, width)), width, t.HunkBg)
	case RowSpacer:
		return ""
	}

	bg, wordBg, sign, signFg := "", "", " ", t.Gutter
	switch row.Line.Kind {
	case diffparse.KindAdd:
		bg, wordBg, sign, signFg = t.AddBg, t.AddWordBg, "+", t.AddFg
	case diffparse.KindDel:
		bg, wordBg, sign, signFg = t.DelBg, t.DelWordBg, "−", t.DelFg
	}
	if cursor {
		bg = t.CursorBg
	}

	var b strings.Builder
	b.WriteString(r.style(t.Gutter, bg).Render(
		num(row.Line.OldNum, r.Doc.gutterOld) + " " + num(row.Line.NewNum, r.Doc.gutterNew) + " "))
	b.WriteString(r.style(signFg, bg).Render(sign))

	codeWidth := width - r.Doc.GutterWidth()
	b.WriteString(r.code(row, codeWidth, hoffset, bg, wordBg))
	return b.String()
}

// code slices the line into display cells so horizontal scrolling, tab
// expansion and wide runes all behave, then coalesces neighbouring cells that
// share styling back into as few escape sequences as possible.
func (r *Renderer) code(row Row, width, hoffset int, bg, wordBg string) string {
	cells := buildCells(row.Segs, row.Marks)

	var b strings.Builder
	col, used := 0, 0
	var runFg string
	var runMark bool
	var run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		cellBg := bg
		if runMark && wordBg != "" {
			cellBg = wordBg
		}
		b.WriteString(r.style(runFg, cellBg).Render(run.String()))
		run.Reset()
	}
	for _, c := range cells {
		if col+c.w <= hoffset {
			col += c.w
			continue
		}
		if used+c.w > width {
			break
		}
		if run.Len() > 0 && (c.fg != runFg || c.mark != runMark) {
			flush()
		}
		runFg, runMark = c.fg, c.mark
		run.WriteRune(c.r)
		col += c.w
		used += c.w
	}
	flush()
	if used < width {
		b.WriteString(r.style("", bg).Render(strings.Repeat(" ", width-used)))
	}
	return b.String()
}

type cell struct {
	r    rune
	w    int
	fg   string
	mark bool
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
					cells = append(cells, cell{r: ' ', w: 1, fg: s.Fg, mark: marked})
				}
				col += n
				byteOff++
				continue
			}
			w := runewidth.RuneWidth(ru)
			if w == 0 {
				w = 1
			}
			cells = append(cells, cell{r: ru, w: w, fg: s.Fg, mark: marked})
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

func (r *Renderer) pad(s string, width int, bg string) string {
	if w := lipgloss.Width(s); w < width {
		return s + r.style("", bg).Render(strings.Repeat(" ", width-w))
	}
	return s
}

func clip(s string, width int) string {
	return runewidth.Truncate(s, width, "…")
}

func num(n, w int) string {
	if n == 0 {
		return strings.Repeat(" ", w)
	}
	return fmt.Sprintf("%*d", w, n)
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
