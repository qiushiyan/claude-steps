package render

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/qiushiyan/claude-steps/internal/record"
)

// Show prints one session with the newest on top: what it is, anything the
// reader could not read, each label's latest event, the rounds with no
// collect seen, the notes, and then its steps. With history, or with no label
// configured, the steps give way to the whole timeline.
func (v View) Show(w io.Writer, s Session, history bool) {
	rec := s.Record
	readable := rec.Status == record.OK || rec.Status == record.Partial

	head := of(plain, v.title(s)).add(plain, "   ").add(faint, short(rec.ID))
	if s.Pane != nil {
		head = head.add(plain, "   "+s.Pane.Where)
	}
	fmt.Fprintln(w, v.paint(head))
	var where, links []string
	if rec.Cwd != "" {
		where = append(where, join(v.path(rec.Cwd), rec.Branch))
	}
	if readable {
		if cs := rec.Compactions(); len(cs) > 0 {
			where = append(where, plural(len(cs), "compaction")+", last "+v.ago(cs[len(cs)-1].At))
		}
		for _, pr := range slices.Backward(rec.PullRequests()) {
			links = append(links, join(linked(pr), pr.URL))
		}
	}
	// One line when it fits, so the labels start a line higher. Otherwise the
	// pull requests start a line of their own: a header with no such line is
	// a session with no pull request.
	under := v.fill(slices.Concat(where, links))
	if len(under) > 1 {
		under = append(v.fill(where), v.fill(links)...)
	}
	for _, l := range under {
		fmt.Fprintln(w, l)
	}
	// What the view may lack is said before anything it holds.
	lack := v.caveat(rec)
	if !readable {
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

	var summary []trow
	if readable {
		if states := record.Summarise(rec.Events, v.Labels); len(states) > 0 {
			rows := make([]trow, 0, len(states))
			for _, l := range states {
				rows = append(rows, v.labelRow(l))
			}
			v.table(w, "", rows, labelWidth)
			fmt.Fprintln(w)
		}
		summary = v.uncollected(rec)
	}
	v.table(w, "", append(summary, v.notes(rec, history)...), textWidth)
	if !readable {
		return
	}

	fmt.Fprintln(w)
	all := v.lines(rec)
	switch {
	case len(all) == 0:
		fmt.Fprintln(w, "no events in the transcript")
	case history || len(v.Labels) == 0:
		fmt.Fprintln(w, v.paint(of(faint, "history")))
		v.timeline(w, collapse(all))
	default:
		fmt.Fprintln(w, v.paint(of(faint, "steps")))
		st := collapse(steps(v.outline(rec)))
		if len(st) == 0 {
			fmt.Fprintln(w, "  "+v.paint(of(faint, "none")))
		}
		v.timeline(w, st)
		fmt.Fprintln(w)
		fmt.Fprintln(w, v.paint(of(faint, plural(len(collapse(all)), "row")+" in the full history (show --all)")))
	}
}

// fill packs the header's items into lines no wider than the view. An item
// moves to the next line whole, so a link is never cut.
func (v View) fill(items []string) []string {
	var lines []string
	for _, it := range items {
		if n := len(lines); n > 0 && (v.Width == 0 || width(lines[n-1])+viewGap+width(it) <= v.Width) {
			lines[n-1] += strings.Repeat(" ", viewGap) + it
			continue
		}
		lines = append(lines, it)
	}
	return lines
}

// labelRow is a label's line in a session view: its name, when its latest
// event happened, the commits since, and the event itself.
func (v View) labelRow(l record.LabelState) trow {
	name := of(v.hue(l.Name), l.Name)
	if l.Latest == nil {
		return trow{lead: []cell{name, of(faint, nothing)}}
	}
	var commits cell
	if n := l.CommitsSince; n != nil {
		commits = of(loud(*n), plural(*n, "commit")+" since")
	}
	// The cell dates a round from its dispatch; the collect says its own time.
	e := *l.Latest
	row := trow{lead: []cell{name, of(plain, v.ago(e.At)), commits}, text: v.describe(e)}
	if e.Kind == record.Round && e.CollectedAt != nil {
		if e.Dispatched {
			row.text = e.Name + "  " + collect(e, v.ago(*e.CollectedAt))
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
func (v View) uncollected(rec record.Record) []trow {
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
		rows = append(rows, trow{lead: []cell{head, of(plain, e.Name), of(plain, v.ago(e.At))}})
		head = cell{}
	}
	return rows
}

// notes are the user's notes as rows of a session view, newest first: the
// latest few, or with all every one. A note is never cut: it is their words.
func (v View) notes(rec record.Record, all bool) []trow {
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
		add(of(plain, v.ago(n.At)), of(plain, oneLine(n.Text)))
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
