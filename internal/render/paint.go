package render

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mattn/go-runewidth"
)

// style is how a piece of text is drawn: an SGR parameter, or none.
//
// Hue is spent on label names, on problems, and on the kind of each item in a
// session's head (its pane, directory, branch and pull requests), and on
// nothing else. Bold is never combined with a hue: the terminal draws bold
// text in the theme's own emphasis colour, and a hue under it would be lost.
type style string

const (
	plain   style = ""
	strong  style = "1"  // bold
	faint   style = "2"  // markers and headings; never a date, a count or a warning
	problem style = "31" // red: an error the transcript reports, or what could not be read
)

// hues are the colours a label's name may take, under the names
// config.Colors offers.
var hues = map[string]style{"blue": "34", "magenta": "35", "cyan": "36", "green": "32", "yellow": "33"}

// cycle is the hue a label takes when it names none, by its place in the
// configuration. Yellow is left out: on a light background it does not carry
// text.
var cycle = []string{"blue", "magenta", "cyan", "green"}

// hue is the style a label's name is drawn in.
func (v View) hue(name string) style {
	for i, l := range v.Labels {
		if l.Name != name {
			continue
		}
		if l.Color != "" {
			return hues[l.Color]
		}
		return hues[cycle[i%len(cycle)]]
	}
	return plain
}

// span is a run of text in one style.
type span struct {
	text  string
	style style
}

// cell is what one column holds in a row. A wide cell does not widen its
// column: it runs over the columns after it, which its row leaves empty.
type cell struct {
	spans []span
	wide  bool
}

func of(st style, text string) cell {
	return cell{}.add(st, text)
}

func (c cell) add(st style, text string) cell {
	if text != "" {
		c.spans = append(c.spans, span{text, st})
	}
	return c
}

// join is c followed by the spans of d.
func (c cell) join(d cell) cell {
	c.spans = append(slices.Clone(c.spans), d.spans...)
	return c
}

func (c cell) width() int {
	n := 0
	for _, s := range c.spans {
		n += width(s.text)
	}
	return n
}

// cut ends a cell at n columns with "…" when it is wider. The mark takes the
// style of the span it falls in, so a cut inside a warning stays a warning.
func (c cell) cut(n int) cell {
	if c.width() <= n {
		return c
	}
	out := cell{wide: c.wide}
	room := n - 1 // the mark takes a column
	for _, s := range c.spans {
		if room < 0 {
			break
		}
		w := width(s.text)
		if w <= room {
			out.spans = append(out.spans, s)
			room -= w
			continue
		}
		kept, at := "", 0
		for i, r := range s.text {
			if at += cells.RuneWidth(r); at > room {
				kept = s.text[:i]
				break
			}
		}
		out.spans = append(out.spans, span{strings.TrimRight(kept, " ") + "…", s.style})
		break
	}
	return out
}

// paint writes a cell with its escapes, or bare when the view has no colour.
func (v View) paint(c cell) string {
	var b strings.Builder
	for _, s := range c.spans {
		if v.Color && s.style != plain {
			b.WriteString("\x1b[" + string(s.style) + "m" + s.text + "\x1b[0m")
			continue
		}
		b.WriteString(s.text)
	}
	return b.String()
}

// starts is the column each cell of rows begins at when they are set gap
// spaces apart, and after the last, where one more would begin. A column no
// row fills takes no room.
func starts(rows [][]cell, gap int) []int {
	var widths []int
	for _, row := range rows {
		for i, c := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			if !c.wide {
				widths[i] = max(widths[i], c.width())
			}
		}
	}
	out := make([]int, len(widths)+1)
	for i, w := range widths {
		out[i+1] = out[i]
		if w > 0 {
			out[i+1] += w + gap
		}
	}
	return out
}

// lay sets rows in columns and paints them. Padding is written outside the
// escapes and a line never ends in a space, so the plain output is the
// coloured output with the escapes removed.
func (v View) lay(rows [][]cell, gap int) []string {
	cols := starts(rows, gap)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		var b strings.Builder
		at := 0
		for i, c := range row {
			w := c.width()
			if w == 0 {
				continue
			}
			to := cols[i]
			if at > 0 {
				to = max(to, at+gap)
			}
			b.WriteString(strings.Repeat(" ", to-at))
			b.WriteString(v.paint(c))
			at = to + w
		}
		lines = append(lines, b.String())
	}
	return lines
}

// trow is a row that ends in free text, which is cut to the room the cells
// before it leave, or with fold set folded onto the rows under it.
type trow struct {
	lead  []cell
	text  string
	style style
	fold  bool
	// ask is a request said before the text, in the columns it is given; ""
	// when it has no room.
	ask func(int) string
}

// table prints rows in columns behind an indent. A row's text takes the room
// left in the view's width, or most columns when the width is not known, and
// is cut to it or folded onto the rows under it. floor is the least room the
// text is given: a row runs past the width by what the floor adds, so a floor
// of one column keeps every row inside it.
func (v View) table(w io.Writer, indent string, rows []trow, most, floor int) {
	leads, cols := make([][]cell, len(rows)), 0
	for i, r := range rows {
		leads[i] = r.lead
		cols = max(cols, len(r.lead))
	}
	room := most
	if v.Width > 0 {
		room = max(v.Width-width(indent)-starts(leads, viewGap)[cols], floor)
	}
	var lines [][]cell
	for _, r := range rows {
		// What ran keeps its room first: the request takes what is left, up
		// to askWidth, and is left out where that is less than askFloor.
		if n := min(askWidth, room-3-min(width(r.text), textFloor)); r.ask != nil && n >= askFloor {
			if ask := r.ask(n); ask != "" {
				r.text = ask + " → " + r.text
			}
		}
		parts := []string{cut(r.text, room)}
		if r.fold && r.text != "" {
			parts = wrap(r.text, room)
		}
		for i, part := range parts {
			lead := make([]cell, cols)
			if i == 0 {
				copy(lead, r.lead)
			}
			lines = append(lines, append(lead, of(r.style, part)))
		}
	}
	for _, line := range v.lay(lines, viewGap) {
		fmt.Fprintln(w, indent+line)
	}
}

// cells measures text as a terminal draws it: a CJK character takes two
// columns and a combining mark none. The condition is fixed here so the
// output does not depend on the locale.
var cells = &runewidth.Condition{StrictEmojiNeutral: true}

func width(s string) int {
	return cells.StringWidth(s)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip puts s on one line and cuts it to n columns.
func clip(s string, n int) string {
	return cut(oneLine(s), n)
}

// wrap breaks s at its spaces into lines of at most n columns. A word wider
// than n is broken across lines, so no word is lost.
func wrap(s string, n int) []string {
	var lines []string
	for _, word := range strings.Fields(s) {
		if k := len(lines); k > 0 && width(lines[k-1])+1+width(word) <= n {
			lines[k-1] += " " + word
			continue
		}
		for width(word) > n {
			at, i := 0, 0
			for j, r := range word {
				if at += cells.RuneWidth(r); at > n {
					i = j
					break
				}
			}
			if i == 0 { // n is narrower than one character
				break
			}
			lines, word = append(lines, word[:i]), word[i:]
		}
		lines = append(lines, word)
	}
	return lines
}

// cut ends s at n columns with "…" when it is longer.
func cut(s string, n int) string {
	if width(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	at := 0
	for i, r := range s {
		w := cells.RuneWidth(r)
		if at+w > n-1 {
			return strings.TrimRight(s[:i], " ") + "…"
		}
		at += w
	}
	return s
}
