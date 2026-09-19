package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
)

// Split layout puts the old file on the left and the new one on the right:
//
//	▎  13 − if err := s.authorize(ctx, r)… │▎  13 + if err := s.authorise(ctx, r, s.policy)…
//	│   │  │ │                              ││
//	│   │  │ └─ code                        │└─ the right pane's own edge marker
//	│   │  └─── sign                        └── the rule between the panes
//	│   └────── old-side line number
//	└────────── edge: the left pane's marker, or the focus bar on the cursor row
//
// Both panes have the same anatomy and the same width, so each states add or
// delete three ways on its own. Everything that is not a line of code — file
// headers, meta rows, annotations — stays full width.

// pairHunk lines a hunk up side by side, GitHub-style: a context line sits on
// both sides of one row, and within each change block the i-th deleted line
// shares a row with the i-th added line. The shorter run is padded with -1,
// which the row draws as filler.
func pairHunk(lines []diffparse.Line) [][2]int {
	var out [][2]int
	i := 0
	context := func(end int) {
		for ; i < end; i++ {
			out = append(out, [2]int{i, i})
		}
	}
	for _, b := range changeBlocks(lines) {
		context(b.del)
		delN, addN := b.add-b.del, b.end-b.add
		for k := 0; k < maxi(delN, addN); k++ {
			p := [2]int{-1, -1}
			if k < delN {
				p[0] = b.del + k
			}
			if k < addN {
				p[1] = b.add + k
			}
			out = append(out, p)
		}
		i = b.end
	}
	context(len(lines))
	return out
}

// IsCode reports whether the row shows diff lines, in either layout.
func (r Row) IsCode() bool { return r.Kind == RowCode || r.Kind == RowPair }

// NewNum is the new-side line number a code row shows: zero for a deleted
// line, and for a split row whose new side is filler.
func (r Row) NewNum() int {
	if r.Kind == RowPair {
		return r.Right.NewNum
	}
	return r.Line.NewNum
}

// LineRow finds the row showing a line of a file, whichever mode the document
// was built in, so a view can keep its cursor on the same line when the
// layout changes underneath it. The new-side number is tried first, since it
// is the coordinate annotations anchor to; the old-side number is the
// fallback for a deleted line, which has no new one. Pass 0 for a number you
// do not have. The second result is false when neither is shown, as in a
// collapsed file.
func (d *Document) LineRow(fileIdx, newNum, oldNum int) (int, bool) {
	if fileIdx < 0 || fileIdx >= len(d.FileRows) {
		return 0, false
	}
	start, end := d.FileRows[fileIdx], len(d.Rows)
	if fileIdx+1 < len(d.FileRows) {
		end = d.FileRows[fileIdx+1]
	}
	find := func(match func(Row) bool) (int, bool) {
		for i := start; i < end; i++ {
			if row := d.Rows[i]; row.IsCode() && match(row) {
				return i, true
			}
		}
		return 0, false
	}
	if newNum > 0 {
		if i, ok := find(func(row Row) bool { return row.NewNum() == newNum }); ok {
			return i, true
		}
	}
	if oldNum > 0 {
		return find(func(row Row) bool { return row.Line.OldNum == oldNum })
	}
	return 0, false
}

// paneNumWidth is the line-number column of both panes. They share one width
// so the panes are mirror images and the code starts at the same offset in
// each.
func (d *Document) paneNumWidth() int { return maxi(d.gutterOld, d.gutterNew) }

// paneGutterWidth is everything in a pane left of its code.
func (d *Document) paneGutterWidth() int {
	// edge + " " + number + " " + sign + " "
	return 2 + d.paneNumWidth() + 3
}

// paneWidth splits a row between two equal panes and the rule between them.
// An odd column left over is padded at the right edge.
func paneWidth(width int) int { return (width - 1) / 2 }

// pairRow paints the old line, the rule, and the new line. One hoffset
// scrolls both panes, so lines that were compared stay aligned.
func (r *Renderer) pairRow(row Row, width, hoffset int, cursor bool) string {
	t := r.Theme
	pw := paneWidth(width)
	if pw <= r.Doc.paneGutterWidth() {
		return r.pad("", width, t.Bg)
	}
	var b strings.Builder
	b.WriteString(r.pane(row.Line, row.Line.OldNum, row.Segs, row.Marks, pw, hoffset, cursor, true, row.Expanded))
	b.WriteString(r.style(t.GutterSep, t.Bg).Render(gutterRule))
	b.WriteString(r.pane(row.Right, row.Right.NewNum, row.RightSegs, row.RightMarks, pw, hoffset, cursor, false, row.Expanded))
	return r.pad(b.String(), width, t.Bg)
}

// pane paints one side of a split row, drawn exactly like a unified row with
// one number column. n is that side's line number; zero makes the pane filler.
// Focus lifts both panes' tones, but only the left pane's edge — the row's
// edge — gives way to the focus bar: the right pane keeps its marker. dim
// paints a line of a Gap, as codeRow does.
func (r *Renderer) pane(ln diffparse.Line, n int, segs []Segment, marks []span, width, hoffset int, focus, rowEdge, dim bool) string {
	t := r.Theme
	if n == 0 {
		bg := t.FillBg
		if focus {
			bg = t.CursorBg
		}
		return r.pad(r.edge(focus && rowEdge, bg), width, bg)
	}
	tn := r.tones(ln.Kind, focus)
	if !rowEdge {
		rest := r.tones(ln.Kind, false)
		tn.edge, tn.edgeFg = rest.edge, rest.edgeFg
	}
	if dim {
		tn.codeFg = t.Dim
	}
	var b strings.Builder
	b.WriteString(r.style(tn.edgeFg, tn.gutter).Render(tn.edge))
	b.WriteString(r.styleKey(styleKey{fg: tn.numFg, bg: tn.gutter, bold: tn.numBold}).
		Render(" " + num(n, r.Doc.paneNumWidth())))
	b.WriteString(r.style(t.GutterSep, tn.gutter).Render(" "))
	b.WriteString(r.bold(tn.signFg, tn.bg).Render(tn.sign))
	b.WriteString(r.style("", tn.bg).Render(" "))
	b.WriteString(r.code(segs, marks, width-r.Doc.paneGutterWidth(), hoffset, tn))
	return b.String()
}

// splitHunkRow gives each pane its half of the header: the old range over the
// old side and the new range over the new, each starting at its pane's code
// column. The section — the enclosing function, usually — follows the old
// range, once; repeating it on the right would say nothing new.
func (r *Renderer) splitHunkRow(row Row, width int, focus bool) string {
	t := r.Theme
	pw := paneWidth(width)
	if pw <= r.Doc.paneGutterWidth() {
		return r.pad(r.edge(focus, t.HunkBg), width, t.HunkBg)
	}
	oldRange, newRange, _ := strings.Cut(row.Detail, " ")
	half := func(edge, name, rng, section string) string {
		var b strings.Builder
		b.WriteString(edge)
		b.WriteString(r.style(t.Dim, t.HunkBg).Render(" " + label(name, r.Doc.paneNumWidth())))
		b.WriteString(r.style("", t.HunkBg).Render("   "))
		room := pw - r.Doc.paneGutterWidth()
		shown := clip(rng, room)
		b.WriteString(r.style(t.Dim, t.HunkBg).Render(shown))
		if rest := room - lipgloss.Width(shown) - 2; section != "" && rest > 0 {
			b.WriteString(r.style("", t.HunkBg).Render("  "))
			b.WriteString(r.style(t.HunkFg, t.HunkBg).Render(clip(section, rest)))
		}
		return r.pad(b.String(), pw, t.HunkBg)
	}
	var b strings.Builder
	b.WriteString(half(r.edge(focus, t.HunkBg), "old", oldRange, row.Text))
	b.WriteString(r.style(t.GutterSep, t.HunkBg).Render(gutterRule))
	b.WriteString(half(r.style("", t.HunkBg).Render(" "), "new", newRange, ""))
	return r.pad(b.String(), width, t.HunkBg)
}
