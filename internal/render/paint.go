package render

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// style is how a piece of text is drawn: an SGR parameter, or none.
//
// Hue is spent on label names and on problems, and on nothing else. Bold is
// never combined with a hue: the terminal draws bold text in the theme's own
// emphasis colour, and a hue under it would be lost.
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

func (c cell) width() int {
	n := 0
	for _, s := range c.spans {
		n += width(s.text)
	}
	return n
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
