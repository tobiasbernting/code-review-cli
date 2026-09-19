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
// Every scene owns the whole screen, and must still name the pull request
// and how to cancel (footer does both); it reports false when the screen is
// too small for it, and the page falls back to its one line.
type scene struct {
	name   string
	themed bool // drawn on the theme's background, not on black
	draw   func(c *canvas, p loadingPage) bool
}

var scenes = []scene{
	{name: "diff", themed: true, draw: drawDiff},
	{name: "boot", draw: drawBoot},
	{name: "linux", draw: drawLinux},
	{name: "penalty", draw: drawPenalty},
	{name: "rain", draw: drawRain},
	{name: "crawl", draw: drawCrawl},
}

// footerRows is how tall footer is.
const footerRows = 4

// footer names the load and the keys on the canvas's last four rows.
func footer(c *canvas, p loadingPage, accent, text, dim string) {
	name := fmt.Sprintf("%s#%d", p.item.Repo, p.item.Number)
	room := maxInt(1, c.w-4-runewidth.StringWidth(name)-2)
	top := c.h - footerRows
	c.clear(0, top, c.w, footerRows)
	at := c.text(2, top, name, accent, true)
	c.text(at, top, "  "+runewidth.Truncate(p.item.Title, room, "…"), text, false)
	if p.item.Author != "" {
		c.text(2, top+1, "by "+p.item.Author, dim, false)
	}
	at = c.text(2, top+2, spinner[p.frame%len(spinner)], accent, false)
	c.text(at, top+2, " fetching the pull request…", dim, false)
	c.text(2, top+3, loadingHint, dim, false)
}

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

// --- diff: a review being written in a full-screen editor, in the theme's
// own diff colours.

// loadingPatch is typed out, a few characters a frame, while the pull
// request loads.
var loadingPatch = []string{
	`diff --git a/review.go b/review.go`,
	`--- a/review.go`,
	`+++ b/review.go`,
	`@@ -1,6 +1,9 @@`,
	` func review(pr *PR) {`,
	`-    skim(pr)`,
	`-    approve(pr) // LGTM`,
	`+    read(pr.Diff)`,
	`+    think()`,
	`+    for _, c := range pr.Changes {`,
	`+        comment(c)`,
	`+    }`,
	` }`,
}

// cardWidth is how wide the rain's clearing lets the title run.
const cardWidth = 36

const (
	typedPerTick = 6  // the whole patch in about the time a load takes
	holdFrames   = 15 // how long the finished patch stays before it restarts
)

func drawDiff(c *canvas, p loadingPage) bool {
	t := p.theme
	if c.w < 40 || c.h < 12 {
		return false
	}
	body := c.h - footerRows - 2 // between the tab bar and the status line
	const gutter = 5

	total := 0
	for _, l := range loadingPatch {
		total += len(l) + 1
	}
	typed := (p.frame * typedPerTick) % (total + holdFrames*typedPerTick)
	done := typed >= total

	at := c.text(1, 0, " review.go ", t.Accent, true)
	if !done {
		c.text(at, 0, "[+]", t.Dim, false)
	}
	c.text(at+4, 0, strings.Repeat("─", maxInt(0, c.w-at-5)), t.Dim, false)

	// What has been typed so far, scrolled like an editor once it passes
	// the bottom of the window.
	type line struct {
		s, colour string
		cursor    bool
	}
	var lines []line
	row, col := 1, 1
	for _, l := range loadingPatch {
		if typed < 0 {
			break
		}
		shown := l
		if typed < len(l) {
			shown = l[:typed]
		}
		colour := t.Fg
		switch {
		case strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "):
			colour = t.Dim
		case strings.HasPrefix(l, "@@"):
			colour = t.Accent
		case strings.HasPrefix(l, "+"):
			colour = t.AddSign
		case strings.HasPrefix(l, "-"):
			colour = t.DelSign
		}
		cursor := typed <= len(l)
		lines = append(lines, line{shown, colour, cursor})
		row, col = len(lines), len(shown)+1
		typed -= len(l) + 1
	}
	from := maxInt(0, len(lines)-body)
	for y := 0; y < body; y++ {
		i := from + y
		if i >= len(lines) {
			c.text(0, 1+y, "~", t.Dim, false)
			continue
		}
		l := lines[i]
		c.text(0, 1+y, fmt.Sprintf("%*d ", gutter-1, i+1), t.Dim, false)
		x := c.text(gutter, 1+y, l.s, l.colour, false)
		if l.cursor && !done && p.frame%6 < 3 {
			c.set(x, 1+y, '▌', t.Accent, false)
		}
	}

	status := c.h - footerRows - 1
	if done {
		c.text(0, status, fmt.Sprintf(`"review.go" %dL written`, len(loadingPatch)), t.Dim, false)
	} else {
		c.text(0, status, "-- INSERT --", t.Accent, true)
	}
	pos := fmt.Sprintf("%d,%d", row, col)
	c.text(c.w-len(pos)-6, status, pos, t.Dim, false)
	c.text(c.w-4, status, "All", t.Dim, false)

	footer(c, p, t.Accent, t.Fg, t.Dim)
	return true
}

// --- boot: a 90s PC starting up, with the pull request as its disk.

func drawBoot(c *canvas, p loadingPage) bool {
	const (
		grey   = "#aaaaaa"
		white  = "#ffffff"
		blue   = "#5555ff"
		yellow = "#ffff55"
		green  = "#55ff55"
	)
	if c.w < 50 || c.h < 14 {
		return false
	}
	type step struct {
		label  string
		takes  int    // frames of dots before the result; 0 prints at once
		result string // what the dots end in
	}
	n := p.item.Number
	steps := []step{
		{"KRV Modular BIOS v2.0, An Energy Star Ally", 0, ""},
		{"Copyright (C) 1987-96, Diff Corp.", 0, ""},
		{"", 0, ""},
		{"KRV-9000 CPU at 133MHz", 0, ""},
		{"", 0, ""}, // the memory count, filled in below
		{"", 0, ""},
		{"KRV Plug and Play BIOS Extension v1.0A", 0, ""},
		{"Detecting IDE Primary Master", 4, "KRV DIFF DRIVE"},
		{"Detecting IDE Primary Slave", 3, "None"},
		{"Detecting IDE Secondary Master", 3, "CD-ROM 52X"},
		{"Detecting IDE Secondary Slave", 3, "None"},
		{"", 0, ""},
		{"Loading KRV.SYS", 4, "OK"},
		{fmt.Sprintf("Mounting PR #%d", n), 5, "OK"},
		{"", 0, ""},
		{fmt.Sprintf("C:\\> krv review #%d", n), 0, ""},
	}
	const (
		memIndex  = 4
		memFrames = 10
		dotsTo    = 34 // the column the dots run out to
	)
	f := p.frame % 90
	area := c.h - footerRows - 2 // the last row is the BIOS's own footer

	var shown []func(y int)
	start := 0
	for i, s := range steps {
		if f < start {
			break
		}
		in := f - start
		switch {
		case i == memIndex:
			kb := minInt(65536, in*65536/memFrames)
			shown = append(shown, func(y int) {
				x := c.text(0, y, fmt.Sprintf("Memory Test :  %5dK", kb), grey, false)
				if kb == 65536 {
					c.text(x, y, " OK", white, true)
				}
			})
			start += memFrames + 2
		case s.takes > 0:
			all := maxInt(3, dotsTo-len(s.label))
			dots, ok := minInt(all, in*all/s.takes), in >= s.takes
			shown = append(shown, func(y int) {
				x := c.text(0, y, s.label+" "+strings.Repeat(".", dots), grey, false)
				if ok {
					colour := white
					if s.result == "OK" {
						colour = green
					}
					c.text(x, y, " "+s.result, colour, true)
				}
			})
			start += s.takes + 1
		case i == len(steps)-1:
			shown = append(shown, func(y int) {
				x := c.text(0, y, s.label, white, false)
				if p.frame%6 < 3 {
					c.set(x, y, '_', white, false)
				}
			})
		case i == 0:
			shown = append(shown, func(y int) { c.text(0, y, s.label, white, true) })
			start += 2
		default:
			shown = append(shown, func(y int) { c.text(0, y, s.label, grey, false) })
			start += 2
		}
	}
	// The screen scrolls once it fills, like a real one.
	from := maxInt(0, len(shown)-area)
	for y, draw := range shown[from:] {
		draw(y)
	}

	// The Energy Star corner, where there is room for it.
	if c.w >= 76 {
		for y, l := range loadingLogo {
			colour := blue
			if y >= 3 {
				colour = yellow
			}
			c.transparent(c.w-len(l)-2, y, l, colour, true)
		}
	}
	c.text(0, area, fmt.Sprintf("09/19/26-KRV9000-PR%d-00", n), grey, false)

	footer(c, p, white, white, grey)
	return true
}

// --- linux: a kernel boots, then someone breaks into the pull request.

func drawLinux(c *canvas, p loadingPage) bool {
	const (
		white = "#d0d0d0"
		grey  = "#6c6c6c"
		green = "#00ff5f"
	)
	if c.w < 50 || c.h < 14 {
		return false
	}
	s := linuxScript(p)
	const hold = 40
	f := p.frame % (s.t + hold)
	area := c.h - footerRows - 1

	var shown []cardLine
	for _, l := range s.lines {
		if l.at > f {
			break
		}
		shown = append(shown, l.draw(f-l.at))
	}
	from := maxInt(0, len(shown)-area)
	for y, l := range shown[from:] {
		at := 0
		for _, sp := range l {
			at = c.text(at, y, sp.s, sp.fg, sp.bold)
		}
	}

	if f >= s.t {
		colour := green
		if (f-s.t)/3%2 == 1 {
			colour = white
		}
		name := fmt.Sprintf("root@%s#%d", p.item.Repo, p.item.Number)
		banner(c, area/2, []string{"A C C E S S   G R A N T E D", name}, colour)
	}

	footer(c, p, green, white, grey)
	return true
}

// banner draws a double-lined box around lines, centred on row y, over
// whatever is there.
func banner(c *canvas, y int, lines []string, colour string) {
	inner := 0
	for _, l := range lines {
		inner = maxInt(inner, runewidth.StringWidth(l))
	}
	inner += 8
	top := y - (len(lines)+4)/2
	x := (c.w - inner - 2) / 2
	c.clear(x-1, top-1, inner+4, len(lines)+6)
	c.text(x, top, "╔"+strings.Repeat("═", inner)+"╗", colour, true)
	c.text(x, top+1, "║"+strings.Repeat(" ", inner)+"║", colour, true)
	for i, l := range lines {
		pad := inner - runewidth.StringWidth(l)
		c.text(x, top+2+i, "║"+strings.Repeat(" ", pad/2)+l+strings.Repeat(" ", pad-pad/2)+"║", colour, i == 0)
	}
	c.text(x, top+2+len(lines), "║"+strings.Repeat(" ", inner)+"║", colour, true)
	c.text(x, top+3+len(lines), "╚"+strings.Repeat("═", inner)+"╝", colour, true)
}

// termLine is one line of a scripted terminal session: it appears at frame
// at, and draw gives it as it looks age frames later.
type termLine struct {
	at   int
	draw func(age int) cardLine
}

// termScript builds a terminal session line by line; t is the frame the
// next line would appear at.
type termScript struct {
	lines []termLine
	t     int
}

func (s *termScript) print(wait int, l cardLine) {
	s.t += wait
	s.lines = append(s.lines, termLine{s.t, func(int) cardLine { return l }})
}

// typed is a command typed at a prompt, two keys a frame.
func (s *termScript) typed(prompt cardLine, cmd, colour string) {
	s.t += 3
	keys := []rune(cmd)
	s.lines = append(s.lines, termLine{s.t, func(age int) cardLine {
		n := minInt(len(keys), age*2)
		l := append(append(cardLine{}, prompt...), span{string(keys[:n]), colour, false})
		if n < len(keys) {
			l = append(l, span{"█", colour, false})
		}
		return l
	}})
	s.t += (len(keys)+1)/2 + 2
}

// progress is a bar that fills over frames, then says so.
func (s *termScript) progress(label string, frames int, bar, text, done string) {
	s.t++
	const width = 24
	s.lines = append(s.lines, termLine{s.t, func(age int) cardLine {
		filled := minInt(width, age*width/frames)
		l := cardLine{{"[*] ", bar, true}, {label + " [", text, false},
			{strings.Repeat("█", filled), bar, false},
			{strings.Repeat("░", width-filled), text, false},
			{fmt.Sprintf("] %3d%%", filled*100/width), text, false}}
		if filled == width {
			l = append(l, span{" done", done, true})
		}
		return l
	}})
	s.t += frames
}

// crack is a secret being guessed: hex that churns every frame, then the
// answer.
func (s *termScript) crack(label, found string, frames int, mark, text, churn, done string) {
	s.t++
	const digits = "0123456789abcdef"
	s.lines = append(s.lines, termLine{s.t, func(age int) cardLine {
		l := cardLine{{"[*] ", mark, true}, {label + " ", text, false}}
		if age >= frames {
			return append(l, span{found, churn, true}, span{" ok", done, true})
		}
		guess := make([]byte, len(found))
		for i := range guess {
			guess[i] = digits[noise(i, age, len(label))%len(digits)]
		}
		return append(l, span{string(guess), churn, false})
	}})
	s.t += frames
}

func linuxScript(p loadingPage) *termScript {
	const (
		white  = "#d0d0d0"
		grey   = "#6c6c6c"
		green  = "#00ff5f"
		red    = "#ff3b3b"
		yellow = "#ffd75f"
		cyan   = "#5fd7ff"
	)
	n, repo := p.item.Number, p.item.Repo
	s := &termScript{}

	// The kernel, two lines a frame.
	kernel := []string{
		fmt.Sprintf("Linux version 6.9.%d-krv (root@diffcorp) (gcc 13.2.0) #%d SMP PREEMPT_DYNAMIC", n%20, n),
		fmt.Sprintf("Command line: BOOT_IMAGE=/vmlinuz-krv root=/dev/pr%d ro quiet nosplash", n),
		"x86/fpu: Supporting XSAVE feature 0x001: 'x87 floating point registers'",
		"BIOS-e820: [mem 0x0000000000000000-0x000000000009fbff] usable",
		"DMI: Diff Corp. KRV-9000/Review Board, BIOS 2.0 09/19/2026",
		"tsc: Detected 4200.000 MHz processor",
		"Memory: 65536K/65536K available (1337K kernel code)",
		"pci 0000:00:1f.2: [8086:2922] type 00 class 0x010601",
		"ahci 0000:00:1f.2: AHCI 0001.0000 32 slots 6 ports 6 Gbps",
		"scsi 0:0:0:0: Direct-Access     KRV      DIFF DRIVE  2.0",
		"sd 0:0:0:0: [sda] Attached SCSI disk",
		"e1000e 0000:00:19.0 eth0: 52:54:00:13:37:00",
		"NET: Registered PF_PACKET protocol family",
		"EXT4-fs (sda1): mounted filesystem with ordered data mode",
		"random: crng init done",
		"audit: apparmor=\"DENIED\" operation=\"ptrace\" profile=\"ci-gatekeeper\"",
		"systemd[1]: systemd 255 running in system mode",
		"systemd[1]: Hostname set to <krv>.",
	}
	usec := 0
	for i, l := range kernel {
		colour := white
		if strings.Contains(l, "DENIED") {
			colour = yellow
		}
		s.print(i%2, cardLine{{fmt.Sprintf("[%5d.%06d] ", usec/1000000, usec%1000000), grey, false}, {l, colour, false}})
		usec += 3000 + noise(i, n)%400000
	}

	ok := func(l string) {
		s.print(1, cardLine{{"[  ", white, false}, {"OK", green, true}, {"  ] ", white, false}, {l, white, false}})
	}
	ok("Started Journal Service.")
	ok(fmt.Sprintf("Mounted /pr/%d.", n))
	ok("Reached target Network.")
	ok("Started OpenSSH Daemon.")
	s.print(1, cardLine{{"[", white, false}, {"FAILED", red, true}, {"] ", white, false}, {"Failed to start CI Gatekeeper.", white, false}})
	ok("Started krv review daemon.")
	s.print(2, nil)

	// Then someone logs in.
	s.typed(cardLine{{"krv login: ", white, false}}, "root", white)
	s.typed(cardLine{{"Password: ", white, false}}, "************", grey)
	s.t += 4
	s.print(0, cardLine{{"Last login: Sat Sep 19 04:20:00 2026 from 10.13.37.1", grey, false}})
	prompt := cardLine{{"root@krv", red, true}, {":", white, false}, {"~", cyan, true}, {"# ", white, false}}
	s.typed(prompt, fmt.Sprintf("./krv --target %s --pr %d --stealth", repo, n), white)

	step := func(wait int, mark, markColour, l, result string) {
		line := cardLine{{mark + " ", markColour, true}, {l, white, false}}
		if result != "" {
			line = append(line, span{" " + result, green, true})
		}
		s.print(wait, line)
	}
	step(2, "[*]", cyan, "resolving github.com ...........", "140.82.121.3")
	step(3, "[*]", cyan, "handshake TLSv1.3 ECDHE-RSA-AES256-GCM ...", "ok")
	s.crack("cracking maintainer token", fmt.Sprintf("ghp_%x", noise(n, 1337)), 12, cyan, white, yellow, green)
	s.progress("bypassing branch protection", 14, cyan, white, green)
	step(2, "[!]", yellow, "CI gatekeeper awake, spoofing status checks ...", "ok")
	step(3, "[*]", cyan, fmt.Sprintf("injecting reviewer into %s#%d ...", repo, n), "ok")
	step(2, "[*]", cyan, "dumping diff", "")

	// The dump is the pull request itself.
	data := []byte(fmt.Sprintf("%s#%d %s by %s", repo, n, p.item.Title, p.item.Author))
	for off := 0; off < minInt(len(data), 96); off += 16 {
		chunk := data[off:minInt(len(data), off+16)]
		var hex, text strings.Builder
		for i := 0; i < 16; i++ {
			if i == 8 {
				hex.WriteString(" ")
			}
			if i >= len(chunk) {
				hex.WriteString("   ")
				continue
			}
			fmt.Fprintf(&hex, "%02x ", chunk[i])
			if b := chunk[i]; b >= 0x20 && b < 0x7f {
				text.WriteByte(b)
			} else {
				text.WriteByte('.')
			}
		}
		s.print(1, cardLine{{fmt.Sprintf("%08x  ", off), grey, false}, {hex.String(), green, false}, {" |" + text.String() + "|", white, false}})
	}
	step(3, "[+]", green, fmt.Sprintf("root on %s#%d", repo, n), "")
	s.t += 3
	return s
}

// --- penalty: the keeper goes the wrong way and it's in the top corner.

func drawPenalty(c *canvas, p loadingPage) bool {
	const (
		white   = "#ffffff"
		grey    = "#8a8f98"
		net     = "#5c6370"
		grassA  = "#2e7d32"
		grassB  = "#43a047"
		keeperC = "#ffd600"
		kickerC = "#e53935"
		board   = "#4fc3f7"
	)
	crowdColours := []string{"#e53935", "#1e88e5", "#fdd835", "#ffffff", "#8e24aa", "#fb8c00"}

	sky := c.h - footerRows - 1
	if c.w < 44 || sky < 16 {
		return false
	}
	crowd := sky / 4
	bar := crowd + 1
	netRows := sky / 3
	line := bar + netRows + 1 // the goal line
	spotX, spotY := c.w/2, sky-2
	gw := minInt(c.w-10, 64)
	left := (c.w - gw) / 2
	right := left + gw - 1
	goalX, goalY := right-3, bar+1

	f := p.frame % 50
	scored := f >= 20

	// The crowd, on its feet once it's in.
	for y := 0; y < crowd; y++ {
		for x := 0; x < c.w; x++ {
			n := noise(x, y, 11)
			if n%5 == 0 {
				continue
			}
			colour := crowdColours[n%len(crowdColours)]
			r := 'o'
			if scored && (f/2+noise(x/3, y))%2 == 0 {
				r = rune("\\o/"[x%3])
			}
			if noise(x, y, p.frame)%150 == 0 {
				r, colour = '*', white // a camera flash
			}
			c.set(x, y, r, colour, false)
		}
	}
	// The advertising boards, scrolling.
	ad := []rune(" KRV · 0 BUGS · REVIEW IT PROPERLY ·")
	for x := 0; x < c.w; x++ {
		colour := board
		if scored && f/3%2 == 0 {
			colour = white
		}
		c.set(x, crowd, ad[(x+p.frame)%len(ad)], colour, true)
	}

	// The pitch, mown in stripes.
	for y := line; y < sky; y++ {
		for x := 0; x < c.w; x++ {
			colour := grassA
			if (x/6)%2 == 0 {
				colour = grassB
			}
			if noise(x, y, 5)%7 == 0 {
				c.set(x, y, '"', colour, false)
			}
		}
	}
	c.text(0, line, strings.Repeat("─", c.w), white, false)

	// The goal: crossbar, posts and a net.
	c.text(left, bar, strings.Repeat("_", right-left+1), white, true)
	for y := bar + 1; y <= bar+netRows; y++ {
		c.set(left, y, '|', white, true)
		c.set(right, y, '|', white, true)
		for x := left + 1; x < right; x++ {
			if (x+y)%2 == 0 {
				c.set(x, y, '.', net, false)
			}
		}
	}

	ball := func(x, y int) { c.set(x, y, 'o', white, true) }
	switch {
	case f < 12: // the run-up: keeper on the line, ball on the spot
		sway := []int{0, 1, 0, -1}[f/3%4]
		keeper(c, spotX+sway, line-3, keeperC)
		ball(spotX+1, spotY)
		kicker(c, spotX-8+f/2, spotY, "/|\\", kickerC)
	case !scored: // the strike, curling away from the dive
		s := f - 12
		x := spotX + 1 + (goalX-spotX-1)*s/7
		y := spotY - (spotY-goalY)*s*(14-s)/49 // rises fast, then drops under the bar
		kicker(c, spotX-2, spotY, "/|_", kickerC)
		diving(c, spotX-(spotX-left-6)*s/7, line-3+minInt(2, s/2), left+2, keeperC)
		ball(x, y)
	default: // in the net
		diving(c, left+6, line-1, left+2, keeperC)
		kicker(c, spotX-2, spotY, "/ \\", kickerC)
		c.transparent(spotX-2, spotY-2, "\\ /", kickerC, true) // arms up
		ripple := f - 20
		for y := bar + 1; y <= bar+netRows && ripple < 16; y++ {
			for x := left + 1; x < right; x++ {
				d := absInt(x-goalX)/3 + absInt(y-goalY)
				if d == ripple || d == ripple-1 {
					c.set(x, y, '#', white, false)
				}
			}
		}
		ball(goalX, goalY)
		if f >= 24 {
			colour := keeperC
			if f/2%2 == 0 {
				colour = grassB
			}
			mid := bar + (netRows+1)/2
			c.centre(mid, "  G O O O A L !  ", colour, true)
			c.centre(mid+1, "  krv 1 - 0 bugs  ", white, false)
		}
	}

	footer(c, p, white, white, grey)
	return true
}

// kicker is the penalty taker from behind, feet on row y.
func kicker(c *canvas, x, y int, legs, colour string) {
	c.transparent(x+1, y-1, "o", colour, true)
	c.transparent(x, y, legs, colour, true)
}

// keeper stands on the line, head on row y.
func keeper(c *canvas, x, y int, colour string) {
	for i, l := range []string{" o ", "\\|/", "/ \\"} {
		c.transparent(x-1, y+i, l, colour, true)
	}
}

// diving is the keeper at full stretch, head first towards the far post,
// never through it.
func diving(c *canvas, x, y, post int, colour string) {
	c.transparent(maxInt(post, x-2), y, "o==<", colour, true)
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
	room := maxInt(1, cardWidth-runewidth.StringWidth(name)-2)
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
	width := minInt(46, c.w-6)
	if width < 34 || c.h < 16 {
		return false
	}
	sky := c.h - footerRows - 1 // rows above the footer

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

	footer(c, p, yellow, "#e6e9ef", grey)
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
