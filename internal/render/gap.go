package render

import "github.com/tobiasbernting/krv/v2/internal/diffparse"

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
