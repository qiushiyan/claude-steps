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
	if !readable(s.Record) {
		return
	}
	fmt.Fprintln(w)
	v.body(w, s.Record, history, true)
}

// Head prints what a session is and where it stands: its title, its
// directory and pull requests, anything the reader could not read, each
// label's latest event, the rounds with no collect seen and the notes. With
// all, every note. A popup shows it beside the timeline, so it fits a narrow
// width too: an item that does not fit a line starts the next, and below
// shortBelow columns a row gives its time as a cell does.
func (v View) Head(w io.Writer, s Session, all bool) {
	rec := s.Record
	ok := readable(rec)

	who := []cell{of(plain, v.title(s)).cut(v.room()), of(faint, short(rec.ID))}
	if s.Pane != nil {
		who = append(who, of(plain, s.Pane.Where))
	}
	var where, links []cell
	if rec.Cwd != "" {
		where = v.split(v.path(rec.Cwd), rec.Branch)
	}
	if ok {
		if cs := rec.Compactions(); len(cs) > 0 {
			where = append(where, of(plain, plural(len(cs), "compaction")+", last "+v.ago(cs[len(cs)-1].At)))
		}
		if !rec.LastAt.IsZero() {
			where = append(where, of(plain, "last message "+v.ago(rec.LastAt)))
		}
		for _, pr := range slices.Backward(rec.PullRequests()) {
			links = append(links, v.split(linked(pr), pr.URL)...)
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
	if lack != "" {
		fmt.Fprintln(w, v.paint(of(problem, lack)))
	}
	rule := ruleWidth
	if v.Width > 0 {
		rule = v.Width
	}
	fmt.Fprintln(w, v.paint(of(faint, strings.Repeat("─", rule))))

	brief := v.Width > 0 && v.Width < shortBelow
	var summary []trow
	if ok {
		if states := record.Summarise(rec.Events, v.Labels); len(states) > 0 {
			rows := make([]trow, 0, len(states))
			for _, l := range states {
				rows = append(rows, v.labelRow(l, brief))
			}
			v.table(w, "", rows, labelWidth)
			fmt.Fprintln(w)
		}
		summary = v.uncollected(rec, brief)
	}
	v.table(w, "", v.wrapNotes(append(summary, v.notes(rec, all, brief)...)), textWidth)
}

// Timeline prints a session's steps, or with history its whole timeline,
// with no head above them and no heading: a popup shows the head beside it
// and names the timeline on its border. A transcript that cannot be read
// says so in the timeline's place.
func (v View) Timeline(w io.Writer, s Session, history bool) {
	if !readable(s.Record) {
		fmt.Fprintln(w, v.paint(of(problem, unread(s.Record))))
		return
	}
	v.body(w, s.Record, history, false)
}

// body is the timeline under its heading: the steps, then how many rows the
// full history holds; or with history, or with no label configured, the
// whole timeline.
func (v View) body(w io.Writer, rec record.Record, history, heading bool) {
	all := v.lines(rec)
	head := func(name string) {
		if heading {
			fmt.Fprintln(w, v.paint(of(faint, name)))
		}
	}
	switch {
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
		fmt.Fprintln(w, v.paint(of(faint, plural(len(collapse(all)), "row")+" in the full history (show --all)")))
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
func (v View) split(head, tail string) []cell {
	if tail == "" || width(join(head, tail)) <= v.room() {
		return []cell{of(plain, join(head, tail))}
	}
	return []cell{of(plain, head), of(plain, tail)}
}

// fill packs the head's items into lines no wider than the view. An item
// moves to the next line whole.
func (v View) fill(items []cell) []cell {
	var lines []cell
	for _, it := range items {
		if n := len(lines); n > 0 && lines[n-1].width()+viewGap+it.width() <= v.room() {
			lines[n-1] = lines[n-1].add(plain, strings.Repeat(" ", viewGap)).join(it)
			continue
		}
		lines = append(lines, it)
	}
	return lines
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
	// The cell dates a round from its dispatch; the collect says its own time.
	e := *l.Latest
	row := trow{lead: []cell{name, of(plain, v.when(e.At, brief)), commits}, text: v.describe(e)}
	if e.Kind == record.Round && e.CollectedAt != nil {
		if e.Dispatched {
			row.text = e.Name + "  " + collect(e, v.sentence(*e.CollectedAt, brief))
		}
		if e.CollectFailed {
			row.style = problem
		}
	}
	row.text = oneLine(row.text)
	return row
}

// uncollected are the rounds dispatched here with no collect in the
// transcript, newest first, as rows of a session view. The heading says what
// was seen and no more: such a round may be running, collected from another
// session, or given up on.
func (v View) uncollected(rec record.Record, brief bool) []trow {
	head := of(faint, "no collect seen")
	out := rec.Uncollected()
	if len(out) == 0 {
		return []trow{{lead: []cell{head, of(faint, nothing)}}}
	}
	var rows []trow
	for i, e := range slices.Backward(out) {
		if len(out)-i > shown {
			rows = append(rows, trow{lead: []cell{{}, of(faint, fmt.Sprintf("%d more", i+1))}})
			break
		}
		rows = append(rows, trow{lead: []cell{head, of(plain, e.Name), of(plain, v.when(e.At, brief))}})
		head = cell{}
	}
	return rows
}

// notes are the user's notes as rows of a session's head, newest first: the
// latest few, or with all every one. A note is never cut: it is their words,
// and wrapNotes folds one that is wider than the view.
func (v View) notes(rec record.Record, all, brief bool) []trow {
	head := of(faint, "notes")
	var rows []trow
	add := func(cells ...cell) {
		rows = append(rows, trow{lead: append([]cell{head}, cells...)})
		head = cell{}
	}
	keep := len(rec.Notes)
	if !all {
		keep = min(keep, shown)
	}
	for i, n := range slices.Backward(rec.Notes) {
		if len(rec.Notes)-i > keep {
			add(of(faint, fmt.Sprintf("%d earlier", i+1)))
			break
		}
		add(of(plain, v.when(n.At, brief)), cell{spans: []span{{oneLine(n.Text), plain}}, note: true})
	}
	if rec.UnreadNotes > 0 {
		add(cell{spans: []span{{plural(rec.UnreadNotes, "note line") + " could not be read", problem}}, wide: true})
	}
	if rec.NotesError != "" {
		add(cell{spans: []span{{rec.NotesError, problem}}, wide: true})
	}
	if len(rows) == 0 {
		add(of(faint, "none"))
	}
	return rows
}

// wrapNotes folds a note wider than the room its column leaves onto the rows
// under it, at its spaces, so the head fits its width and the note keeps
// every word.
func (v View) wrapNotes(rows []trow) []trow {
	if v.Width <= 0 {
		return rows
	}
	leads := make([][]cell, len(rows))
	for i, r := range rows {
		leads[i] = r.lead
	}
	var out []trow
	for _, r := range rows {
		last := len(r.lead) - 1
		if last < 0 || !r.lead[last].note {
			out = append(out, r)
			continue
		}
		room := max(v.Width-starts(leads, viewGap)[last], textFloor)
		for i, part := range wrap(r.lead[last].spans[0].text, room) {
			lead := slices.Clone(r.lead)
			if i > 0 {
				lead = make([]cell, last+1)
			}
			lead[last] = of(plain, part)
			out = append(out, trow{lead: lead})
		}
	}
	return out
}
