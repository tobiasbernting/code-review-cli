package render

import (
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
)

// preview regenerates the theme screenshots the README and docs/rendering.md
// embed:
//
//	go test ./internal/render -run TestWritePreviewSVG -preview docs/img
//
// It is a test only because that is where the renderer, the fixture and the
// overlay already live. It writes SVG rather than a terminal capture so the
// screenshots are diffable, need no font on the reader's machine, and show
// the true colours a 24-bit terminal would.
var preview = flag.String("preview", "", "write theme screenshots to this directory, relative to the repository root")

const (
	previewCols   = 100
	previewCharW  = 8.4
	previewLineH  = 19.0
	previewPad    = 14.0
	previewSize   = 14.0
	previewBase   = 13.5 // baseline within a line box
	previewFamily = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'DejaVu Sans Mono', monospace"
)

func TestWritePreviewSVG(t *testing.T) {
	if *preview == "" {
		t.Skip("pass -preview <dir> to regenerate the theme screenshots")
	}
	dir := filepath.Join("..", "..", *preview)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// lipgloss decides once, from the process's stdout, whether colour is
	// even possible; the golden tests want it off, so put it back.
	defer lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetColorProfile(termenv.TrueColor)

	files := denseFiles(t)
	ov := denseOverlay()

	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		doc := Build(files, NewHighlighter(th.Syntax, true), ov, Layout{})
		r := NewRenderer(th, doc)

		var lines []string
		for _, row := range doc.Rows {
			// Park the cursor on the added line, so the screenshot shows
			// what focus does to the grid.
			focus := row.Kind == RowCode && row.Line.Kind == diffparse.KindAdd && row.Line.NewNum == 13
			// maxLines 0: annotations expand, the way the plain-text path
			// prints them, so the screenshot shows a wrapped conversation.
			lines = append(lines, r.RenderLines(row, previewCols, 0, focus, 0)...)
		}

		path := filepath.Join(dir, "theme-"+name+".svg")
		if err := os.WriteFile(path, []byte(ansiToSVG(lines, th.Bg)), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
}

// ansiToSVG paints ANSI-styled lines as SVG: one rect per background run, one
// text element per foreground run, every run given an explicit textLength so
// the grid holds whatever font the reader's browser picks.
func ansiToSVG(lines []string, bg string) string {
	width := previewCols*previewCharW + 2*previewPad
	height := float64(len(lines))*previewLineH + 2*previewPad

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="%s" font-size="%.0f">`,
		width, height, width, height, html.EscapeString(previewFamily), previewSize)
	fmt.Fprintf(&b, "\n<rect width=\"%.0f\" height=\"%.0f\" rx=\"6\" fill=\"%s\"/>", width, height, bg)

	for i, line := range lines {
		top := previewPad + float64(i)*previewLineH
		col := 0.0
		for _, run := range parseANSI(line) {
			w := float64(cells(run.text)) * previewCharW
			x := previewPad + col
			if run.bg != "" {
				fmt.Fprintf(&b, "\n<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"%s\"/>",
					x, top, w, previewLineH, run.bg)
			}
			if trimmed := strings.TrimRight(run.text, " "); trimmed != "" {
				weight := ""
				if run.bold {
					weight = ` font-weight="600"`
				}
				deco := ""
				if run.underline {
					deco = ` text-decoration="underline"`
				}
				fill := run.fg
				if fill == "" {
					fill = "#cccccc"
				}
				fmt.Fprintf(&b, "\n<text x=\"%.2f\" y=\"%.2f\" fill=\"%s\" textLength=\"%.2f\" lengthAdjust=\"spacingAndGlyphs\"%s%s xml:space=\"preserve\">%s</text>",
					x, top+previewBase, fill, w, weight, deco, html.EscapeString(run.text))
			}
			col += w
		}
	}
	b.WriteString("\n</svg>\n")
	return b.String()
}

type ansiRun struct {
	text            string
	fg, bg          string
	bold, underline bool
}

var ansiRe = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// parseANSI turns one styled line back into runs. Only the escapes lipgloss
// emits for these themes are handled — 24-bit colour, bold, underline, reset —
// because that is all the renderer ever produces.
func parseANSI(line string) []ansiRun {
	var runs []ansiRun
	var cur ansiRun
	pos := 0
	for _, m := range ansiRe.FindAllStringSubmatchIndex(line, -1) {
		if text := line[pos:m[0]]; text != "" {
			run := cur
			run.text = text
			runs = append(runs, run)
		}
		pos = m[1]
		codes := strings.Split(line[m[2]:m[3]], ";")
		for i := 0; i < len(codes); i++ {
			switch codes[i] {
			case "", "0":
				cur = ansiRun{}
			case "1":
				cur.bold = true
			case "4":
				cur.underline = true
			case "38", "48":
				if i+4 < len(codes) && codes[i+1] == "2" {
					colour := fmt.Sprintf("#%02x%02x%02x", atoiSafe(codes[i+2]), atoiSafe(codes[i+3]), atoiSafe(codes[i+4]))
					if codes[i] == "38" {
						cur.fg = colour
					} else {
						cur.bg = colour
					}
					i += 4
				}
			}
		}
	}
	if text := line[pos:]; text != "" {
		run := cur
		run.text = text
		runs = append(runs, run)
	}
	return runs
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// cells counts the terminal columns a run occupies. Every glyph the renderer
// draws is single-width, so a rune count is the column count.
func cells(s string) int { return len([]rune(s)) }
