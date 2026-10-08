package render

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/record"
)

// Show prints one session with the newest on top: its head, and then its
// steps. With history, or with no label configured, the steps give way to
// the whole timeline.
func (v View) Show(w io.Writer, s Session, history bool) {
	v.Head(w, s, history)
	if !readable(s.Record) && len(s.Record.Notes) == 0 {
		return
	}
	fmt.Fprintln(w)
	v.body(w, s.Record, history, true)
}

// Head prints what a session is and where it stands: its title, its
// directory and pull requests, anything the reader could not read, each
// label's latest event and the notes. With all, every note. A popup shows it
// beside the timeline, or above it when the popup is small, so it fits a
// narrow width whole: an item that does not fit a line starts the next, one
// wider than the width is folded, a row's text is cut to the room it has, and
// below shortBelow columns a row gives its time as a cell does.
func (v View) Head(w io.Writer, s Session, all bool) {
	rec := s.Record
	ok := readable(rec)

	who := []cell{of(strong, v.title(s)).cut(v.room()), of(faint, short(rec.ID))}
	if s.Pane != nil {
		who = append(who, v.mark(paneIcon, hues["cyan"], plain, s.Pane.Where))
	}
	var where, links []cell
	if rec.Cwd != "" {
		where = append(where, v.mark(dirIcon, hues["blue"], hues["blue"], v.path(rec.Cwd)))
	}
	if rec.Branch != "" {
		where = append(where, v.mark(branchIcon, hues["magenta"], hues["magenta"], rec.Branch))
	}
	if ok {
		if cs := rec.Compactions(); len(cs) > 0 {
			where = append(where, v.mark(compactIcon, faint, plain, plural(len(cs), "compaction")+", last "+v.ago(cs[len(cs)-1].At)))
		}
		if !rec.LastAt.IsZero() {
			where = append(where, v.mark(clockIcon, faint, plain, "last message "+v.ago(rec.LastAt)))
		}
		for _, pr := range slices.Backward(rec.PullRequests()) {
			links = append(links, v.split(v.mark(prIcon, hues["green"], hues["green"], linked(pr)), pr.URL)...)
		}
	}
	// The place and the pull requests share a line when it fits, so the
	// labels start a line higher. Otherwise the pull requests start a line of
	// their own: a head with no such line is a session with no pull request.
	under := v.fill(slices.Concat(where, links))
	if len(under) > 1 {
		under = append(v.fill(where), v.fill(links)...)
	}
	for _, l := range append(v.fill(who), under...) {
		fmt.Fprintln(w, v.paint(l))
	}
	// What the view may lack is said before anything it holds.
	lack := v.caveat(rec)
	if !ok {
		lack = join(unread(rec), v.path(rec.Path))
	}
	v.warn(w, lack)
	rule := ruleWidth
	if v.Width > 0 {
		rule = v.Width
	}
	fmt.Fprintln(w, v.paint(of(faint, strings.Repeat("─", rule))))

	brief := v.Width > 0 && v.Width < shortBelow
	if ok {
		if states := record.Summarise(rec.Events, v.Labels); len(states) > 0 {
			rows := make([]trow, 0, len(states))
			for _, l := range states {
				rows = append(rows, v.labelRow(l, brief))
			}
			v.table(w, "", rows, labelWidth, 1)
			fmt.Fprintln(w)
		}
	}
	v.table(w, "", v.notes(rec, all, brief), textWidth, 1)
}

// On a terminal the head marks its items with Nerd Font glyphs, as the shell
// prompt and the tmux bar do, so each kind of fact is found by its mark and
// its hue. A glyph is drawn with the colour: plain output has neither, since
// whatever reads it may lack the font.
const (
	paneIcon    = "\uf120" // a terminal
	dirIcon     = "\uf07c" // an open folder
	branchIcon  = "\ue0a0" // a branch
	compactIcon = "\uf066" // arrows pointing in
	clockIcon   = "\uf017" // a clock
	prIcon      = "\uf407" // a pull request
)

// mark is a head item behind its glyph, each in its own style.
func (v View) mark(icon string, glyph, st style, text string) cell {
	if !v.Color {
		return of(plain, text)
	}
	return of(glyph, icon+" ").add(st, text)
}

// warn says what the view may lack, folded to the width: it is never cut.
func (v View) warn(w io.Writer, lack string) {
	if lack == "" {
		return
	}
	for _, l := range v.fold(of(problem, lack)) {
		fmt.Fprintln(w, v.paint(l))
	}
}

// Timeline prints a session's steps, or with history its whole timeline,
// with no head above them and no heading: a popup shows the head beside it
// and names the timeline on its border. What the view may lack is said
// first all the same, so the timeline stands on its own; a transcript that
// cannot be read says so, above the notes it leaves.
func (v View) Timeline(w io.Writer, s Session, history bool) {
	if readable(s.Record) {
		v.warn(w, v.caveat(s.Record))
	} else {
		fmt.Fprintln(w, v.paint(of(problem, unread(s.Record))))
	}
	v.body(w, s.Record, history, false)
}

// body is the timeline under its heading: the steps, then how many rows the
// full history holds and how many labelled reads only it shows; or with
// history, or with no label configured, the whole timeline. A transcript that cannot be read leaves the notes, which
// outlive it, and nothing else.
func (v View) body(w io.Writer, rec record.Record, history, heading bool) {
	all := v.lines(rec)
	// The heading says the order: a list read from the top otherwise reads as
	// the order things ran in.
	head := func(name string) {
		if heading {
			fmt.Fprintln(w, v.paint(of(faint, name+" · newest first")))
		}
	}
	switch {
	case len(all) == 0 && !readable(rec):
	case len(all) == 0:
		fmt.Fprintln(w, "no events in the transcript")
	case history || len(v.Labels) == 0:
		head("history")
		v.timeline(w, collapse(all))
	default:
		head("steps")
		st := collapse(steps(v.outline(rec)))
		if len(st) == 0 {
			fmt.Fprintln(w, "  "+v.paint(of(faint, "none")))
		}
		v.timeline(w, st)
		fmt.Fprintln(w)
		foot := plural(len(collapse(all)), "row") + " in the full history (show --all)"
		// A label's read nothing asked for is left to the history, which
		// says so here: the label's `·` may stand over a stage asked for in
		// words the reader does not match.
		if n := record.ReadsLeftOut(rec.Events, v.Labels); n > 0 {
			foot += ", with " + plural(n, "read") + " of a skill's file that no step shows"
		}
		for _, l := range v.fold(of(faint, foot)) {
			fmt.Fprintln(w, v.paint(l))
		}
	}
}

func readable(rec record.Record) bool {
	return rec.Status == record.OK || rec.Status == record.Partial
}

// room is the columns a line may fill, or as many as it takes when the width
// is not known.
func (v View) room() int {
	if v.Width > 0 {
		return v.Width
	}
	return math.MaxInt
}

// split is a head item and what follows it: one item when the two fit a
// line, two when they do not, so a link moves to a line of its own and is
// never cut.
func (v View) split(head cell, tail string) []cell {
	if tail == "" {
		return []cell{head}
	}
	if head.width()+2+width(tail) <= v.room() {
		return []cell{head.add(plain, "  "+tail)}
	}
	return []cell{head, of(plain, tail)}
}

// fill packs the head's items into lines no wider than the view. An item
// moves to the next line whole, and one folded across lines shares none.
func (v View) fill(items []cell) []cell {
	var lines []cell
	open := false // the last line can take another item
	for _, it := range items {
		parts := v.fold(it)
		if n := len(lines); open && len(parts) == 1 && lines[n-1].width()+viewGap+it.width() <= v.room() {
			lines[n-1] = lines[n-1].add(plain, strings.Repeat(" ", viewGap)).join(it)
			continue
		}
		lines = append(lines, parts...)
		open = len(parts) == 1
	}
	return lines
}

// fold breaks an item wider than the view into lines that fit, at its spaces
// where it has them, so a link, a path or a warning is never cut. The item's
// last span is folded; the spans before it (a mark) open the first line, and
// the lines after start under the text.
func (v View) fold(c cell) []cell {
	if c.width() <= v.room() || len(c.spans) == 0 {
		return []cell{c}
	}
	lead := cell{spans: slices.Clone(c.spans[:len(c.spans)-1])}
	last := c.spans[len(c.spans)-1]
	if lead.width() >= v.Width {
		lead = cell{}
	}
	var out []cell
	for i, part := range wrap(last.text, v.Width-lead.width()) {
		if i == 0 {
			out = append(out, lead.add(last.style, part))
			continue
		}
		out = append(out, of(plain, strings.Repeat(" ", lead.width())).add(last.style, part))
	}
	return out
}

// when is a time in a head's row: in words, or as a cell gives it when the
// head is narrow.
func (v View) when(at time.Time, brief bool) string {
	if brief {
		return v.brief(at)
	}
	return v.ago(at)
}

// labelRow is a label's line in a session's head: its name, when its latest
// event happened, the commits since, and the event itself. A brief row gives
// the time and the count as the board's cell does.
func (v View) labelRow(l record.LabelState, brief bool) trow {
	name := of(v.hue(l.Name), l.Name)
	if l.Latest == nil {
		return trow{lead: []cell{name, of(faint, nothing)}}
	}
	var commits cell
	if n := l.CommitsSince; n != nil {
		commits = of(loud(*n), plural(*n, "commit")+" since")
		if brief {
			commits = of(loud(*n), fmt.Sprintf("+%d", *n))
		}
	}
	// A round is dated at its dispatch, the code its reviewer read.
	e := *l.Latest
	row := trow{lead: []cell{name, of(plain, v.when(e.At, brief)), commits}, text: v.describe(e)}
	if e.Kind == record.Round {
		row.text, row.style = round(e)
	}
	row.text = oneLine(row.text)
	return row
}

// more says how many rows a list left out. It runs over the columns after
// it, so the time column keeps the width of a time.
func more(text string) cell {
	return cell{spans: []span{{text, faint}}, wide: true}
}

// notes are the user's notes as rows of a session's head, newest first: the
// latest few, or with all every one. A note is one line here, cut to the
// room it has; the steps keep its every word.
func (v View) notes(rec record.Record, all, brief bool) []trow {
	head := of(faint, "notes")
	var rows []trow
	add := func(r trow) {
		r.lead = append([]cell{head}, r.lead...)
		rows = append(rows, r)
		head = cell{}
	}
	// What could not be read is folded, never cut.
	warn := func(text string) { add(trow{text: text, style: problem, fold: true}) }
	keep := len(rec.Notes)
	if !all {
		keep = min(keep, shown)
	}
	for i, n := range slices.Backward(rec.Notes) {
		if len(rec.Notes)-i > keep {
			add(trow{lead: []cell{more(fmt.Sprintf("%d earlier", i+1))}})
			break
		}
		add(trow{lead: []cell{of(plain, v.when(n.At, brief))}, text: oneLine(n.Text)})
	}
	if rec.UnreadNotes > 0 {
		warn(plural(rec.UnreadNotes, "note line") + " could not be read")
	}
	if rec.NotesError != "" {
		warn(rec.NotesError)
	}
	if len(rows) == 0 {
		add(trow{lead: []cell{of(faint, "none")}})
	}
	return rows
}
