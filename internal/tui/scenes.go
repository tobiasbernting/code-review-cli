package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// A scene is one of the loading page's animations. One is picked at random
// for each load, and tab steps through the rest.
//
// A boxed scene draws into the small box under the logo. A full scene owns
// the whole screen, and must still name the pull request and how to cancel;
// it reports false when the screen is too small for it, and the page falls
// back to its one line.
type scene struct {
	name  string
	title string // the box border's label; boxed scenes only
	full  bool
	draw  func(c *canvas, p loadingPage) bool
}

var scenes = []scene{
	{name: "diff", title: "reviewing", draw: drawDiff},
	{name: "boot", title: "booting", draw: drawBoot},
	{name: "penalty", title: "penalty", draw: drawPenalty},
	{name: "rain", full: true, draw: drawRain},
	{name: "crawl", full: true, draw: drawCrawl},
}

const boxRows = 7

// space is behind the full scenes, whatever the theme: rain and starfields
// only read on black.
const space = "#000000"

// cell is one character of a canvas.
type cell struct {
	r    rune
	fg   string
	bold bool
}

// canvas is a grid of single-width cells that scenes draw on; rows turns it
// into styled lines.
type canvas struct {
	w, h  int
	cells []cell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: maxInt(0, w), h: maxInt(0, h)}
	c.cells = make([]cell, c.w*c.h)
	c.clear(0, 0, c.w, c.h)
	return c
}

func (c *canvas) set(x, y int, r rune, fg string, bold bool) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y*c.w+x] = cell{r: r, fg: fg, bold: bold}
}

func (c *canvas) clear(x, y, w, h int) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.set(i, j, ' ', "", false)
		}
	}
}

// text writes s from x on row y, clipped to the canvas. Spaces in s are
// drawn too, so text blots out what was under it.
func (c *canvas) text(x, y int, s, fg string, bold bool) int {
	for _, r := range s {
		c.set(x, y, r, fg, bold)
		x++
	}
	return x
}

// transparent is text whose spaces leave what is under them alone, for
// sprites flying over a background.
func (c *canvas) transparent(x, y int, s, fg string, bold bool) {
	for _, r := range s {
		if r != ' ' {
			c.set(x, y, r, fg, bold)
		}
		x++
	}
}

func (c *canvas) centre(y int, s, fg string, bold bool) {
	c.text((c.w-runewidth.StringWidth(s))/2, y, s, fg, bold)
}

// rows renders the canvas on bg, one styled string per row, each exactly w
// wide. Cells that share a style go out as one run.
func (c *canvas) rows(bg string) []string {
	styles := map[cell]lipgloss.Style{}
	style := func(k cell) lipgloss.Style {
		k.r = 0
		if s, ok := styles[k]; ok {
			return s
		}
		s := lipgloss.NewStyle().Background(lipgloss.Color(bg)).Bold(k.bold)
		if k.fg != "" {
			s = s.Foreground(lipgloss.Color(k.fg))
		}
		styles[k] = s
		return s
	}
	out := make([]string, c.h)
	var b, run strings.Builder
	for y := 0; y < c.h; y++ {
		b.Reset()
		row := c.cells[y*c.w : (y+1)*c.w]
		for i := 0; i < len(row); {
			run.Reset()
			j := i
			for j < len(row) && row[j].fg == row[i].fg && row[j].bold == row[i].bold {
				run.WriteRune(row[j].r)
				j++
			}
			b.WriteString(style(row[i]).Render(run.String()))
			i = j
		}
		out[y] = b.String()
	}
	return out
}

// noise is a cheap, stable hash: the same arguments give the same number on
// every frame, so scenes can scatter things without keeping state.
func noise(vs ...int) int {
	h := uint32(2166136261)
	for _, v := range vs {
		h ^= uint32(v)
		h *= 16777619
		h ^= h >> 13
	}
	return int(h & 0x7fffffff)
}

// --- diff: a review being written, typed out in the theme's diff colours.

func drawDiff(c *canvas, p loadingPage) bool {
	t := p.theme
	total := 0
	for _, l := range loadingHunk {
		total += len(l) + 1
	}
	typed := (p.frame * typedPerTick) % (total + holdFrames*typedPerTick)

	cursorShown := false
	for y, l := range loadingHunk {
		shown := l
		if typed < len(l) {
			shown = l[:maxInt(0, typed)]
		}
		typed -= len(l) + 1

		colour := t.Fg
		switch {
		case strings.HasPrefix(l, "@@"):
			colour = t.Accent
		case strings.HasPrefix(l, "+"):
			colour = t.AddSign
		case strings.HasPrefix(l, "-"):
			colour = t.DelSign
		}
		x := c.text(0, y, shown, colour, false)
		if !cursorShown && typed < 0 {
			cursorShown = true
			if p.frame%6 < 3 {
				c.set(x, y, '▌', t.Accent, false)
			}
		}
	}
	return true
}

// --- boot: a 90s PC starting up, with the pull request as its disk.

func drawBoot(c *canvas, p loadingPage) bool {
	t := p.theme
	type step struct {
		label string
		takes int // frames of dots before OK; 0 prints at once
	}
	steps := []step{
		{"KRV BIOS v2.0  (C) 1987 Diff Corp.", 0},
		{"", 0}, // the memory count, filled in below
		{"CPU: 6502 @ 1.02 MHz", 2},
		{"Detecting diff drive", 3},
		{"Loading KRV.SYS", 3},
		{fmt.Sprintf("Mounting PR #%d", p.item.Number), 4},
		{fmt.Sprintf("C:\\> krv review #%d", p.item.Number), 0},
	}
	// About three seconds to the prompt, which then blinks a while.
	const memFrames = 6
	f := p.frame % 60

	var shown []func(y int)
	start := 0
	for i, s := range steps {
		if f < start {
			break
		}
		in := f - start
		switch {
		case i == 1:
			kb := minInt(640, in*640/memFrames)
			shown = append(shown, func(y int) {
				x := c.text(0, y, fmt.Sprintf("Memory test: %3dK", kb), t.Fg, false)
				if kb == 640 {
					c.text(x, y, " OK", t.AddSign, true)
				}
			})
			start += memFrames + 2
		case s.takes > 0:
			// Dots run out to the edge, then the OK lands.
			all := maxInt(3, c.w-len(s.label)-4)
			dots, ok := minInt(all, in*all/s.takes), in >= s.takes
			shown = append(shown, func(y int) {
				x := c.text(0, y, s.label+" "+strings.Repeat(".", dots), t.Fg, false)
				if ok {
					c.text(x, y, " OK", t.AddSign, true)
				}
			})
			start += s.takes + 1
		case i == len(steps)-1:
			shown = append(shown, func(y int) {
				x := c.text(0, y, s.label, t.Accent, false)
				if p.frame%6 < 3 {
					c.set(x, y, '▌', t.Accent, false)
				}
			})
		default:
			shown = append(shown, func(y int) { c.text(0, y, s.label, t.Dim, true) })
			start += 3
		}
	}
	// The screen scrolls once it fills, like a real one.
	from := maxInt(0, len(shown)-c.h)
	for y, draw := range shown[from:] {
		draw(y)
	}
	return true
}

// --- penalty: the keeper goes the wrong way and it's in the top corner.

func drawPenalty(c *canvas, p loadingPage) bool {
	t := p.theme
	const (
		left, right = 3, 32 // the posts
		spotX       = 17
	)
	f := p.frame % 50

	// The goal: crossbar, posts and a net.
	c.text(left, 0, strings.Repeat("_", right-left+1), t.Fg, true)
	for y := 1; y <= 4; y++ {
		c.set(left, y, '|', t.Fg, true)
		c.set(right, y, '|', t.Fg, true)
		for x := left + 1; x < right; x++ {
			if (x+y)%2 == 0 {
				c.set(x, y, '.', t.Dim, false)
			}
		}
	}
	for x := 0; x < c.w; x++ {
		if noise(x, 5)%3 == 0 {
			c.set(x, 5, '"', t.AddSign, false)
		}
	}

	// Where the ball ends up: top right, just under the bar.
	goalX, goalY := right-3, 1
	ball := func(x, y int) { c.set(x, y, 'o', t.Fg, true) }

	switch {
	case f < 12: // the run-up: keeper on the line, ball on the spot
		sway := []int{0, 1, 0, -1}[f/3%4]
		keeper(c, spotX+sway, t.Accent)
		ball(spotX+1, 6)
		kicker(c, spotX-8+f/2, "/|\\", t.DelSign)
	case f < 20: // the strike, curling away from the dive
		s := f - 12
		x := spotX + 1 + (goalX-spotX-1)*s/7
		y := 6 - (6-goalY)*s*(14-s)/49       // rises fast, then drops under the bar
		kicker(c, spotX-2, "/|_", t.DelSign) // follow-through
		diving(c, spotX-2*s, minInt(4, 2+s/2), t.Accent)
		ball(x, y)
	default: // in the net
		diving(c, spotX-16, 4, t.Accent)
		ripple := f - 20
		for y := 1; y <= 4 && ripple < 10; y++ {
			for x := left + 1; x < right; x++ {
				d := absInt(x-goalX)/2 + absInt(y-goalY)
				if d == ripple/2 || d == ripple/2-1 {
					c.set(x, y, '#', t.Fg, false)
				}
			}
		}
		ball(goalX, goalY)
		if f >= 24 {
			colour := t.Accent
			if f/2%2 == 0 {
				colour = t.AddSign
			}
			c.centre(2, " G O O O A L ! ", colour, true)
			c.centre(3, " krv 1 - 0 bugs ", t.Fg, false)
		}
	}
	return true
}

// kicker is the penalty taker from behind, on the edge of the box.
func kicker(c *canvas, x int, legs, colour string) {
	c.transparent(x+1, 5, "o", colour, true)
	c.transparent(x, 6, legs, colour, true)
}

func keeper(c *canvas, x int, colour string) {
	for i, l := range []string{" o ", "\\|/", "/ \\"} {
		c.transparent(x-1, 2+i, l, colour, true)
	}
}

// diving is the keeper at full stretch, head first towards the far post.
func diving(c *canvas, x, y int, colour string) {
	c.transparent(maxInt(4, x-2), y, "o==<", colour, true)
}

// --- rain: the matrix, all over the screen, with the load in a clearing.

const rainGlyphs = "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789:=*+<>|"

var rainRunes = []rune(rainGlyphs)

func drawRain(c *canvas, p loadingPage) bool {
	const (
		head   = "#e8ffe8"
		bright = "#5fff87"
		mid    = "#00c040"
		dark   = "#007a28"
		faint  = "#003d14"
	)
	for x := 0; x < c.w; x++ {
		speed := 1 + noise(x, 1)%2
		length := 5 + noise(x, 2)%maxInt(1, c.h*2/3)
		cycle := c.h + length + noise(x, 3)%maxInt(1, c.h)
		top := (p.frame*speed + noise(x, 4)) % cycle
		for d := 0; d < length; d++ {
			y := top - d
			if y < 0 || y >= c.h {
				continue
			}
			// Glyphs flicker, each at its own pace.
			r := rainRunes[noise(x, y, p.frame/(2+noise(x, y)%4))%len(rainRunes)]
			colour, bold := faint, false
			switch {
			case d == 0:
				colour, bold = head, true
			case d < 3:
				colour = bright
			case d < length/2:
				colour = mid
			case d < length*4/5:
				colour = dark
			}
			c.set(x, y, r, colour, bold)
		}
	}

	card := p.card(bright, mid, dark)
	w, h := card.size()
	if w+8 > c.w || h+4 > c.h {
		return false
	}
	x, y := (c.w-w)/2, (c.h-h)/2
	c.clear(x-3, y-1, w+6, h+2)
	card.draw(c, x, y)
	return true
}

// card is the logo and what is loading, as the rain's clearing shows it.
type cardLine []span

type span struct {
	s    string
	fg   string
	bold bool
}

type cardLines []cardLine

func (p loadingPage) card(bright, text, dim string) cardLines {
	name := fmt.Sprintf("%s#%d", p.item.Repo, p.item.Number)
	var lines cardLines
	for _, l := range loadingLogo {
		lines = append(lines, cardLine{{l, bright, true}})
	}
	room := maxInt(1, hunkWidth-runewidth.StringWidth(name)-2)
	lines = append(lines, nil,
		cardLine{{name, bright, true}, {"  " + runewidth.Truncate(p.item.Title, room, "…"), text, false}})
	if p.item.Author != "" {
		lines = append(lines, cardLine{{"by " + p.item.Author, dim, false}})
	}
	lines = append(lines, nil,
		cardLine{{spinner[p.frame%len(spinner)], bright, false}, {" fetching the pull request…", dim, false}},
		nil,
		cardLine{{loadingHint, dim, false}},
	)
	return lines
}

func (l cardLine) width() int {
	w := 0
	for _, s := range l {
		w += runewidth.StringWidth(s.s)
	}
	return w
}

func (ls cardLines) size() (w, h int) {
	for _, l := range ls {
		w = maxInt(w, l.width())
	}
	return w, len(ls)
}

// draw writes the card with each line centred in its own width.
func (ls cardLines) draw(c *canvas, x, y int) {
	w, _ := ls.size()
	for i, l := range ls {
		at := x + (w-l.width())/2
		for _, s := range l {
			at = c.text(at, y+i, s.s, s.fg, s.bold)
		}
	}
}

// --- crawl: a long time ago, in a repo far, far away.

func drawCrawl(c *canvas, p loadingPage) bool {
	const (
		yellow = "#ffe81f"
		gold   = "#a89400"
		blue   = "#4bd5ee"
		grey   = "#8a8f98"
		red    = "#ff3b30"
	)
	const footer = 4
	width := minInt(46, c.w-6)
	if width < 34 || c.h < 16 {
		return false
	}
	sky := c.h - footer - 1 // rows above the footer

	// Stars, a few of them twinkling.
	for y := 0; y < sky; y++ {
		for x := 0; x < c.w; x++ {
			switch n := noise(x, y) % 60; {
			case n == 0:
				c.set(x, y, '.', grey, false)
			case n == 1 && noise(x, y, p.frame/5)%4 != 0:
				c.set(x, y, '*', "#d0d4dc", false)
			}
		}
	}

	const intro = 12
	f := p.frame
	if f < intro {
		c.centre(sky/2-1, "A long time ago in a repo far,", blue, false)
		c.centre(sky/2, "far, far away....", blue, false)
	} else {
		lines := crawlText(p, width)
		// One row every other frame, bottom to top, then round again.
		pos := (f - intro) / 2 % (len(lines) + sky)
		for i, l := range lines {
			y := sky - pos + i
			if y < 0 || y >= sky {
				continue
			}
			colour := yellow
			if y < sky/3 { // far away, and fading
				colour = gold
			}
			c.centre(y, l.s, colour, l.bold)
		}
	}

	// Traffic: a TIE patrol one way, an X-wing chasing a TIE the other.
	lane := func(k int) int { return 1 + noise(k, 7)%maxInt(1, sky-2) }
	period := c.w + 30
	for k, speed := range []int{1, 2} {
		n := p.frame*speed + noise(k, 8)
		x := c.w + 10 - n%period
		c.transparent(x, lane(k*31+n/period), "|-o-|", grey, true)
	}
	n := p.frame*2 + noise(9)
	x := n%period - 20
	y := lane(99 + n/period)
	c.transparent(x+14, y, "|-o-|", grey, true)
	c.transparent(x, y, "=-[X>", "#e6e9ef", true)
	if bolt := p.frame % 4; bolt < 3 {
		c.transparent(x+6+bolt*3, y, "==", red, true)
	}

	// The footer: what is loading and how to get out.
	name := fmt.Sprintf("%s#%d", p.item.Repo, p.item.Number)
	room := maxInt(1, c.w-4-runewidth.StringWidth(name)-2)
	top := c.h - footer
	at := c.text(2, top, name, yellow, true)
	c.text(at, top, "  "+runewidth.Truncate(p.item.Title, room, "…"), "#e6e9ef", false)
	if p.item.Author != "" {
		c.text(2, top+1, "by "+p.item.Author, grey, false)
	}
	at = c.text(2, top+2, spinner[p.frame%len(spinner)], yellow, false)
	c.text(at, top+2, " fetching the pull request…", grey, false)
	c.text(2, top+3, loadingHint, grey, false)
	return true
}

type crawlLine struct {
	s    string
	bold bool
}

func crawlText(p loadingPage, width int) []crawlLine {
	var lines []crawlLine
	for _, l := range loadingLogo {
		lines = append(lines, crawlLine{l, true})
	}
	lines = append(lines,
		crawlLine{},
		crawlLine{"Episode " + roman(p.item.Number), false},
		crawlLine{"A NEW REVIEW", true},
		crawlLine{},
	)
	story := fmt.Sprintf("It is a period of code review. Rebel developers, striking from a hidden branch, "+
		"have opened pull request #%d against %s. During the battle, a lone reviewer has fetched its diff: "+
		"\u201c%s\u201d. Pursued by sinister CI agents, the reviewer races home aboard krv, custodian of "+
		"the changes that can save the codebase and restore freedom to main....",
		p.item.Number, p.item.Repo, p.item.Title)
	for _, l := range wrapWords(story, width) {
		lines = append(lines, crawlLine{l, false})
	}
	return lines
}

// roman numbers an episode; big pull requests just get their number.
func roman(n int) string {
	if n <= 0 || n >= 4000 {
		return fmt.Sprint(n)
	}
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var b strings.Builder
	for i, v := range vals {
		for n >= v {
			b.WriteString(syms[i])
			n -= v
		}
	}
	return b.String()
}

// wrapWords breaks s into lines at most width wide, cutting words that are
// longer than a line.
func wrapWords(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		for runewidth.StringWidth(w) > width {
			if line != "" {
				lines, line = append(lines, line), ""
			}
			cut := runewidth.Truncate(w, width, "")
			lines, w = append(lines, cut), w[len(cut):]
		}
		switch {
		case line == "":
			line = w
		case runewidth.StringWidth(line)+1+runewidth.StringWidth(w) <= width:
			line += " " + w
		default:
			lines, line = append(lines, line), w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
