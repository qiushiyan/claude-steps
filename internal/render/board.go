package render

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/qiushiyan/claude-steps/internal/record"
)

// labelCell is a label's board cell: when its latest event happened, with
// "+N" for the commits made since where the label counts them. It is one of
//
//	·   <time>   <time> +N   read <time>   read <time> +N
//	pasted <time>   pasted <time> +N   named <time>
//
// and nothing else: a cell carries a date, never a judgement. A read and a
// paste are said, so a request never stands as a run.
func (v View) labelCell(l record.LabelState) cell {
	if l.Latest == nil {
		return of(faint, nothing)
	}
	when := v.brief(l.Latest.At)
	switch l.Latest.Kind {
	case record.Mention:
		return of(plain, "named "+when)
	case record.Read:
		when = "read " + when
	case record.Snippet:
		when = "pasted " + when
	}
	c := of(plain, when)
	if n := l.CommitsSince; n != nil {
		c = c.add(plain, " ").add(loud(*n), fmt.Sprintf("+%d", *n))
	}
	return c
}

// uncollectedCell is the same fact on the board: when the newest such round
// was dispatched, and how many there are when more than one.
func (v View) uncollectedCell(rec record.Record) cell {
	out := rec.Uncollected()
	if len(out) == 0 {
		return of(faint, nothing)
	}
	c := of(plain, v.brief(out[len(out)-1].At))
	if len(out) > 1 {
		c = c.add(plain, fmt.Sprintf(" ×%d", len(out)))
	}
	return c
}

// boardRow is one session on the board before its title and note are cut to
// the room there is.
type boardRow struct {
	cells  []cell // every column but the note; cells[1] waits for the title
	title  string
	caveat string // what the view may lack, said before the note
	note   string // the latest note
}

// Board prints one row per session. With ids, every line starts with the
// pane id, a tab, the session id and a tab, both empty on the header line. A
// picker hides the two fields and acts on them, so a preview or a note goes
// to the session the row showed, whatever the pane runs by then.
//
// When the width is known the title and the note give way to it and the label
// cells never do. A note with no room is left to the session view.
func (v View) Board(w io.Writer, sessions []Session, ids bool) {
	header := []cell{of(plain, "pane"), of(plain, "session")}
	for _, l := range v.Labels {
		header = append(header, of(v.hue(l.Name), l.Name))
	}
	header = append(header, of(plain, "no collect"), of(plain, "PR"))

	rows := make([]boardRow, len(sessions))
	fixed := [][]cell{slices.Replace(slices.Clone(header), 1, 2, cell{})}
	titleW, noteW := width("session"), 0
	for i, s := range sessions {
		rec := s.Record
		r := boardRow{title: v.title(s), cells: []cell{{}, {}}}
		if s.Pane != nil {
			r.cells[0] = of(plain, s.Pane.Where)
		}
		if n := len(rec.Notes); n > 0 {
			r.note = clip(rec.Notes[n-1].Text, noteWidth)
			if n > 1 {
				r.note = fmt.Sprintf("(%d) %s", n, r.note)
			}
		}
		if rec.Status == record.Missing || rec.Status == record.Unreadable {
			r.cells = append(r.cells, cell{spans: []span{{unread(rec), problem}}, wide: true})
		} else {
			for _, l := range record.Summarise(rec.Events, v.Labels) {
				r.cells = append(r.cells, v.labelCell(l))
			}
			prs := of(faint, nothing)
			if list := rec.PullRequests(); len(list) > 0 {
				var numbers []string
				for _, pr := range list {
					numbers = append(numbers, fmt.Sprintf("#%d", pr.Number))
				}
				prs = of(plain, strings.Join(numbers, " "))
			}
			r.cells = append(r.cells, v.uncollectedCell(rec), prs)
			r.caveat = v.caveat(rec)
		}
		for len(r.cells) < len(header) {
			r.cells = append(r.cells, cell{})
		}
		titleW = max(titleW, min(width(r.title)+r.mark().width(), titleWidth))
		noteW = max(noteW, r.noted().width())
		rows[i] = r
		fixed = append(fixed, r.cells)
	}
	if noteW > 0 {
		noteW = max(noteW, width("note"))
	}
	if v.Width > 0 {
		// Every other column keeps its width. The title gives way first, then
		// the note, and a note with no room left goes.
		rest := starts(fixed, boardGap)[len(header)]
		over := func() int {
			total := rest + titleW
			if noteW > 0 {
				total += boardGap + noteW
			}
			return total - v.Width
		}
		shrink := func(w *int, floor int) {
			if n := over(); n > 0 && *w > floor {
				*w = max(floor, *w-n)
			}
		}
		shrink(&titleW, 20)
		shrink(&noteW, 16)
		shrink(&titleW, 12)
		if over() > 0 {
			noteW = 0
		}
	}

	table := make([][]cell, 0, len(rows)+1)
	if noteW > 0 {
		header = append(header, of(plain, "note"))
	}
	table = append(table, header)
	for _, r := range rows {
		r.cells[1] = r.mark().add(plain, oneLine(r.title)).cut(titleW)
		if noteW > 0 {
			r.cells = append(r.cells, r.noted().cut(noteW))
		}
		table = append(table, r.cells)
	}
	for i, line := range v.lay(table, boardGap) {
		if ids {
			pane, id := "", ""
			if i > 0 {
				id = sessions[i-1].Record.ID
				if p := sessions[i-1].Pane; p != nil {
					pane = p.ID
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", pane, id, line)
			continue
		}
		fmt.Fprintln(w, line)
	}
}

// mark leads the title of a session whose transcript was read with something
// missing. The words are in the note cell and on the session view; the mark
// is what stays when the note has no room.
func (r boardRow) mark() cell {
	if r.caveat == "" {
		return cell{}
	}
	return of(problem, "!").add(plain, " ")
}

// noted is the row's note cell before it is cut to the room there is: what
// the view may lack, then the latest note.
func (r boardRow) noted() cell {
	if r.caveat == "" {
		return of(plain, r.note)
	}
	c := of(problem, "["+r.caveat+"]")
	if r.note != "" {
		c = c.add(plain, " "+r.note)
	}
	return c
}
