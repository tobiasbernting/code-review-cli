package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
)

// Gap is a run of unchanged lines a diff does not show: between two hunks,
// before the first or after the last. Unchanged means both sides have them,
// so a Gap is numbered on each.
type Gap struct {
	// Index is the hunk the Gap comes before; the number of hunks for the
	// Gap after the last.
	Index    int
	OldStart int
	NewStart int
	Lines    int
}

// Gaps finds a file's Gaps from its hunk headers. newLines is the length of
// the file's new side, or 0 when it is not known — and then the Gap after the
// last hunk is left out, because whether the file goes on is not known
// either.
func Gaps(f *diffparse.FileDiff, newLines int) []Gap {
	if shownWhole(f) {
		return nil
	}
	hunks := f.Hunks()
	if len(hunks) == 0 {
		return nil
	}
	var out []Gap
	oldNext, newNext := 1, 1
	for i, h := range hunks {
		if n := before(h.NewStart, h.NewLines) - newNext + 1; n > 0 {
			out = append(out, Gap{Index: i, OldStart: oldNext, NewStart: newNext, Lines: n})
		}
		oldNext = before(h.OldStart, h.OldLines) + h.OldLines + 1
		newNext = before(h.NewStart, h.NewLines) + h.NewLines + 1
	}
	if n := newLines - newNext + 1; newLines > 0 && n > 0 {
		out = append(out, Gap{Index: len(hunks), OldStart: oldNext, NewStart: newNext, Lines: n})
	}
	return out
}

// fileGaps is one file's Gaps by Index, with how far each is open.
type fileGaps struct {
	byIndex map[int]Gap
	exp     Expansion
}

// gaps is nil when the overlay leaves Gaps out.
func (o Overlay) gaps(f *diffparse.FileDiff) *fileGaps {
	if o.Expansion == nil {
		return nil
	}
	exp := o.Expansion(f.Path())
	fg := &fileGaps{byIndex: map[int]Gap{}, exp: exp}
	for _, g := range Gaps(f, len(exp.Text)) {
		fg.byIndex[g.Index] = g
	}
	return fg
}

// gap draws the Gap before hunk index, if there is one: the lines shown from
// its top, a row for what is still left out, then the lines shown from its
// bottom. It reports whether there was one to draw.
func (d *Document) gap(fi int, fg *fileGaps, index int) bool {
	if fg == nil {
		return false
	}
	g, ok := fg.byIndex[index]
	if !ok {
		return false
	}
	var top, bottom int
	if fg.exp.Text != nil {
		shown := fg.exp.Shown[index]
		top = mini(maxi(shown.Top, 0), g.Lines)
		bottom = mini(maxi(shown.Bottom, 0), g.Lines-top)
	}
	for i := 0; i < top; i++ {
		d.expandedLine(fi, g, i, fg.exp.Text)
	}
	if hidden := g.Lines - top - bottom; hidden > 0 {
		rest := Gap{Index: index, OldStart: g.OldStart + top, NewStart: g.NewStart + top, Lines: hidden}
		d.Rows = append(d.Rows, Row{Kind: RowGap, FileIdx: fi, HunkIdx: -1, Gap: &rest})
	}
	for i := g.Lines - bottom; i < g.Lines; i++ {
		d.expandedLine(fi, g, i, fg.exp.Text)
	}
	return true
}

// expandedLine draws line i of a Gap as an unchanged line, on both sides in
// split. Text that ends before the Gap does — a file that changed after the
// diff was taken — shows what there is.
func (d *Document) expandedLine(fi int, g Gap, i int, text []string) {
	n := g.NewStart + i
	if n > len(text) {
		return
	}
	ln := diffparse.Line{Kind: diffparse.KindContext, Text: text[n-1], OldNum: g.OldStart + i, NewNum: n}
	d.trackGutter(ln)
	segs := []Segment{{Text: ln.Text}}
	row := Row{Kind: RowCode, FileIdx: fi, HunkIdx: -1, Line: ln, Segs: segs, Expanded: true}
	if d.Layout.Mode == ModeSplit {
		row.Kind, row.Right, row.RightSegs = RowPair, ln, segs
	}
	d.Rows = append(d.Rows, row)
}

// MayContinue reports whether a file could have lines after its last hunk,
// which only its content can settle. A last line followed by "\ No newline at
// end of file" settles it without.
func MayContinue(f *diffparse.FileDiff) bool {
	if shownWhole(f) {
		return false
	}
	hunks := f.Hunks()
	if len(hunks) == 0 {
		return false
	}
	last := hunks[len(hunks)-1].Lines
	for i := len(last) - 1; i >= 0; i-- {
		if last[i].Kind != diffparse.KindDel {
			return !last[i].NoNewline
		}
	}
	return true
}

// shownWhole is a file whose diff leaves nothing out — added or deleted — or
// shows none of it.
func shownWhole(f *diffparse.FileDiff) bool {
	return f.IsBinary || f.Status == diffparse.Added || f.Status == diffparse.Deleted
}

// before is the last line ahead of one side of a hunk. A side with no lines
// is numbered from the line before it rather than from its first.
func before(start, lines int) int {
	if lines == 0 {
		return start
	}
	return start - 1
}

// gapRow stands in for the lines a Gap still leaves out. It is drawn in the
// gutter's colours, with the ⋯ in the sign column and the count where code
// would start, so it reads as part of the margin rather than as a line of the
// file.
func (r *Renderer) gapRow(row Row, width int, focus bool) string {
	t := r.Theme
	bg := t.GutterBg
	if focus {
		bg = t.CursorBg
	}
	var b strings.Builder
	b.WriteString(r.edge(focus, bg))
	if r.Doc.Layout.Mode == ModeSplit {
		b.WriteString(r.style("", bg).Render(" " + strings.Repeat(" ", r.Doc.paneNumWidth()) + " "))
	} else {
		b.WriteString(r.style("", bg).Render(" " + strings.Repeat(" ", r.Doc.gutterOld)))
		b.WriteString(r.style(t.GutterSep, bg).Render(" " + gutterRule + " "))
		b.WriteString(r.style("", bg).Render(strings.Repeat(" ", r.Doc.gutterNew) + " "))
	}
	label := "⋯ " + gapLabel(row.Gap)
	b.WriteString(r.style(t.LineNumFg, bg).Render(clip(label, width-lipgloss.Width(b.String()))))
	return r.pad(b.String(), width, bg)
}

// gapLabel counts what a Gap row leaves out.
func gapLabel(g *Gap) string {
	if g == nil {
		return ""
	}
	return fmt.Sprintf("%d unchanged line%s", g.Lines, pluralWord(g.Lines))
}
