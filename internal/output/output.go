// Package output prints picpull's progress for people: a progress bar and
// colors on a terminal, plain lines when piped.
package output

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

type Level int

const (
	Quiet Level = iota
	Normal
	Verbose
)

type Counts struct{ Success, Skip, Fail int }

func (c *Counts) Add(o Counts) {
	c.Success += o.Success
	c.Skip += o.Skip
	c.Fail += o.Fail
}

type ImageEvent int

const (
	Downloaded ImageEvent = iota
	Cached
	Skipped
	Failed
)

const (
	green     = "\x1b[32m"
	yellow    = "\x1b[33m"
	red       = "\x1b[31m"
	reset     = "\x1b[0m"
	clearLine = "\r\x1b[K"
	barWidth  = 28
)

type Printer struct {
	out, errw  io.Writer
	level      Level
	tty, color bool
	width      int
	barShown   bool
}

func New(out, errw io.Writer, level Level, tty, color bool, width int) *Printer {
	return &Printer{out: out, errw: errw, level: level, tty: tty, color: color, width: width}
}

// styled is text that already carries the printer's own color codes; line
// leaves it alone while it cleans every plain string argument.
type styled string

func (p *Printer) paint(code, s string) styled {
	s = clean(s)
	if !p.color {
		return styled(s)
	}
	return styled(code + s + reset)
}

// clean replaces control characters and bidi overrides with a space. Page
// titles, URLs and error texts come from the page, and a raw escape sequence
// in them could rewrite the terminal.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) ||
			(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return ' '
		}
		return r
	}, s)
}

func (p *Printer) line(w io.Writer, format string, args ...any) {
	// The bar has no newline; the next line would otherwise be appended to it.
	if p.barShown {
		fmt.Fprint(p.out, clearLine)
		p.barShown = false
	}
	for i, a := range args {
		if str, ok := a.(string); ok {
			args[i] = clean(str)
		}
	}
	fmt.Fprintf(w, format+"\n", args...)
}

func (p *Printer) PageStart(index int, multi bool, title, host string) {
	if p.level == Quiet {
		return
	}
	prefix := "▸ "
	if multi {
		prefix += fmt.Sprintf("[%03d] ", index)
	}
	title, host = clean(title), clean(host)
	suffix := " — " + host
	header := prefix + p.fit(title, displayWidth(prefix+suffix)) + suffix
	// On a terminal too narrow even for the shortened title, cut the whole line.
	if p.tty && p.width > 0 {
		header = truncate(header, p.width)
	}
	p.line(p.out, "%s", header)
}

// fit cuts title so the whole header stays on one terminal line. Piped
// output keeps the full title.
func (p *Printer) fit(title string, used int) string {
	if !p.tty || p.width <= 0 {
		return title
	}
	return truncate(title, max(p.width-used, 10))
}

// truncate cuts s to at most room columns, ending it with an ellipsis.
func truncate(s string, room int) string {
	if displayWidth(s) <= room {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > room-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

func (p *Printer) PageURL(url string) {
	if p.level == Verbose {
		p.line(p.out, "  URL %s", url)
	}
}

func (p *Printer) Found(n int) {
	if p.level == Quiet {
		return
	}
	if n == 1 {
		p.line(p.out, "  Found 1 image")
	} else {
		p.line(p.out, "  Found %d images", n)
	}
}

func (p *Printer) NoImages() {
	p.Warn("No images found. Try --all-network or check filters.")
}

func (p *Printer) Warn(msg string) {
	if p.level == Quiet {
		return
	}
	p.line(p.errw, "  %s %s", p.paint(yellow, "!"), msg)
}

// Progress redraws the bar in place. Verbose mode prints a line per image
// instead, and piped output gets nothing so logs stay free of control codes.
func (p *Printer) Progress(done, total int) {
	if !p.tty || p.level != Normal || total <= 0 {
		return
	}
	count := fmt.Sprintf("%d/%d", done, total)
	size := barWidth
	if p.width > 0 {
		// Two spaces on each side of the bar; a bar that fills the last
		// column would wrap.
		size = min(size, p.width-len(count)-5)
	}
	size = max(size, 0)
	filled := size * done / total
	bar := strings.Repeat("━", filled) + strings.Repeat("─", size-filled)
	fmt.Fprintf(p.out, "%s  %s  %s", clearLine, bar, count)
	p.barShown = true
}

func (p *Printer) Image(ev ImageEvent, url, detail string) {
	if p.level != Verbose {
		return
	}
	switch ev {
	case Downloaded:
		p.line(p.out, "  downloading %s → %s", url, detail)
	case Cached:
		p.line(p.out, "  cached      %s → %s", url, detail)
	case Skipped:
		p.line(p.out, "  skip        %s (%s)", url, detail)
	case Failed:
		p.line(p.errw, "  %s %s: %s", p.paint(red, "failed     "), url, detail)
	}
}

func (p *Printer) PageSummary(c Counts) {
	if p.level == Quiet {
		return
	}
	p.line(p.out, "  %s %d success · %d skip · %d fail", p.paint(green, "✓"), c.Success, c.Skip, c.Fail)
}

func (p *Printer) Stopped(reason string, isErr bool) {
	if isErr {
		p.line(p.errw, "Stopped: %s", reason)
		return
	}
	if p.level != Quiet {
		p.line(p.out, "Stopped: %s", reason)
	}
}

func (p *Printer) Error(msg string) {
	p.line(p.errw, "Error: %s", msg)
}

func (p *Printer) Interrupted() {
	p.line(p.errw, "Interrupted.")
}

func (p *Printer) Done(pages int, multi bool, c Counts, failed []string) {
	if p.level != Quiet {
		p.line(p.out, "")
	}
	counts := fmt.Sprintf("%d success · %d skip · %d fail", c.Success, c.Skip, c.Fail)
	switch {
	case !multi:
		p.line(p.out, "Done · %s", counts)
	case pages == 1:
		p.line(p.out, "Done · 1 page · %s", counts)
	default:
		p.line(p.out, "Done · %d pages · %s", pages, counts)
	}
	if len(failed) > 0 {
		p.line(p.errw, "Failed:")
		for _, u := range failed {
			p.line(p.errw, "  %s", p.paint(red, u))
		}
	}
}

// runeWidth is the number of terminal columns r takes: East Asian wide
// characters and emoji are two, combining marks and joiners are zero.
func runeWidth(r rune) int {
	switch {
	case r == 0xfe0f:
		// Emoji presentation selector: the preceding symbol renders as two
		// columns, one of which it already counted.
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case r >= 0x1f300 && r <= 0x1faff:
		return 2
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}
