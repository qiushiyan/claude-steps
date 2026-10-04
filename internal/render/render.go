// Package render turns records into the text and JSON the command prints.
// Its functions are pure: the current time, the home directory, the width to
// fill and whether to colour come in through View.
//
// Nothing here says a check is finished or still holds. A line states what the
// transcript holds and when it happened, and colour follows the same rule: a
// hue names a label or marks a problem, and never grades a date.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/panes"
	"github.com/qiushiyan/claude-steps/internal/record"
)

// Session is one session as a view shows it: the record, the pane it runs in
// when it is live, and its state under each configured label.
type Session struct {
	Pane   *panes.Pane
	Record record.Record
	Labels []record.LabelState
}

type View struct {
	Now  time.Time
	Home string // shown as "~" in paths
	// Labels are the configured labels in order: the board's columns, the
	// tags on a session's steps, and the hue each name is drawn in.
	Labels []config.Label
	// Width is the columns a line may fill, or 0 when that is not known.
	Width int
	// Color writes escape codes.
	Color bool
}

const (
	titleWidth = 34 // a session's title on the board, at most
	noteWidth  = 48 // a note on the board when the width is not known
	labelWidth = 70 // a label's latest event when the width is not known
	textWidth  = 96 // an event's text when the width is not known
	textFloor  = 24 // the least room an event's text is cut to
	ruleWidth  = 72 // the rule under the header when the width is not known
	shown      = 3  // the notes and the uncollected rounds a session view lists
	viewGap    = 3
	boardGap   = 2
	nothing    = "·"
)

func (v View) path(p string) string {
	if v.Home != "" {
		if rest, ok := strings.CutPrefix(p, v.Home); ok && (rest == "" || rest[0] == '/') {
			return "~" + rest
		}
	}
	return p
}

// describe is the text of one event, without its time.
func (v View) describe(e record.Event) string {
	switch e.Kind {
	case record.Skill:
		line := "/" + e.Name
		if e.Via == "tool" {
			line = "skill " + e.Name
		}
		if e.Failed {
			return line + "  failed to load"
		}
		return join(line, clip(e.Args, textWidth))
	case record.Read:
		return "read skills/" + e.Name + "/SKILL.md"
	case record.Snippet:
		return "pasted " + e.Name
	case record.Mention:
		return fmt.Sprintf("you: %q", e.Text)
	case record.Round:
		if e.CollectedAt != nil {
			return e.Name + "  " + collect(e, "")
		}
		return e.Name + "  " + dispatch(e)
	case record.Commit:
		head := "commit"
		if e.Amend {
			head = "commit (amend)"
		}
		if e.Dir != "" {
			head += " in " + v.path(e.Dir)
		}
		return join(head, e.Text)
	case record.PR:
		how := "linked"
		if e.OpenedHere {
			how = "opened here"
		}
		return join(fmt.Sprintf("PR #%d %s", e.Number, how), e.Repo)
	case record.Compaction:
		if e.Trigger != "" {
			return "compaction (" + e.Trigger + ")"
		}
		return "compaction"
	case record.Note:
		return oneLine(e.Text)
	}
	return string(e.Kind)
}

// collect says how a round's collect went, with when after the verb.
// envoy's "ok" says the job returned a result, not that a review passed, so
// it is not printed. Any other word is envoy's own.
func collect(e record.Event, when string) string {
	switch {
	case e.Outcome == "error":
		return join("collect returned an error", when)
	case e.Outcome != "" && e.Outcome != "ok":
		return join("collected", when) + ", envoy said " + e.Outcome
	}
	return join("collected", when)
}

func dispatch(e record.Event) string {
	if e.Failed {
		return "run returned an error"
	}
	return "dispatched"
}

// line is one dated row of a session's timeline.
type line struct {
	at     time.Time
	kind   record.Kind
	labels []string // the labels the event is under
	text   string
	style  style
	times  int  // how many equal lines in a row this one stands for
	count  bool // a count of commits, which has no time of its own
}

// round is a round's one line among the steps: its name, and what the
// transcript holds of it only where that is something other than one dispatch
// and a collect that returned a result.
func round(e record.Event, dispatches int) (string, style) {
	var said []string
	if dispatches > 1 {
		said = append(said, fmt.Sprintf("dispatched %d times", dispatches))
	}
	st := plain
	switch {
	case e.Failed:
		said, st = append(said, dispatch(e)), problem
	case e.CollectedAt == nil:
		said = append(said, "no collect seen")
	default:
		switch {
		case e.Outcome == "error":
			said, st = append(said, "collect returned an error"), problem
		case e.Outcome != "" && e.Outcome != "ok":
			said = append(said, "envoy said "+e.Outcome)
		}
		if !e.Dispatched {
			said = append(said, "no dispatch seen")
		}
	}
	return join(e.Name, strings.Join(said, ", ")), st
}

// outline is the timeline the steps are picked from, oldest first. Under a
// label that lists rounds a step is a round, so a round is one line here,
// dated at its dispatch. It carries the latest skill run before it under its
// label, whose own line it replaces, and stands for the dispatches the
// session replaced under its name. The two are joined by order alone. The
// full history keeps each of those at its own time.
func (v View) outline(rec record.Record) []line {
	var out []line
	asked := map[string]int{} // label → the line of its latest skill run, until a round takes it
	taken := map[int]bool{}   // lines a round replaced
	again := map[string]int{} // round name → its dispatches a later one replaced
	for _, e := range rec.Timeline() {
		l := line{at: e.At, kind: e.Kind, labels: record.LabelsOf(v.Labels, e), text: v.describe(e)}
		if e.Failed {
			l.style = problem
		}
		switch {
		case e.Kind == record.Round && e.Redispatched && !e.Failed:
			again[e.Name]++
			continue
		case e.Kind == record.Round:
			l.text, l.style = round(e, again[e.Name]+1)
			delete(again, e.Name)
			if !e.Dispatched || e.Failed {
				break
			}
			for _, name := range l.labels {
				if i, ok := asked[name]; ok {
					l.text = join(l.text, out[i].text)
					taken[i] = true
					maps.DeleteFunc(asked, func(_ string, at int) bool { return at == i })
					break
				}
			}
		case e.Kind == record.Skill && !e.Failed:
			for _, name := range l.labels {
				asked[name] = len(out)
			}
		}
		out = append(out, l)
	}
	kept := out[:0]
	for i, l := range out {
		if !taken[i] {
			kept = append(kept, l)
		}
	}
	return kept
}

// lines are a session's whole timeline, oldest first. A round this session
// dispatched and collected is two lines, each at its own time.
func (v View) lines(rec record.Record) []line {
	var out []line
	for _, e := range rec.Timeline() {
		l := line{at: e.At, kind: e.Kind, labels: record.LabelsOf(v.Labels, e), text: v.describe(e)}
		if e.Failed {
			l.style = problem
		}
		if e.Kind == record.Round && e.CollectedAt != nil {
			got := l
			got.at, got.style = *e.CollectedAt, plain
			if e.Outcome == "error" {
				got.style = problem
			}
			if !e.Dispatched {
				out = append(out, got)
				continue
			}
			l.text = e.Name + "  " + dispatch(e)
			out = append(out, l, got)
			continue
		}
		out = append(out, l)
	}
	slices.SortStableFunc(out, func(a, b line) int { return a.at.Compare(b.at) })
	return out
}

// steps keeps the lines read first: those under a label, and the notes. The
// commits between two of them become one count line, and so do the commits
// before the first and after the last. Every other line is left to the full
// history.
func steps(all []line) []line {
	var out []line
	commits := 0
	flush := func() {
		if commits > 0 {
			out = append(out, line{kind: record.Commit, text: plural(commits, "commit"), count: true})
			commits = 0
		}
	}
	for _, l := range all {
		switch {
		case l.kind == record.Commit:
			commits++
		case len(l.labels) > 0 || l.kind == record.Note:
			flush()
			out = append(out, l)
		}
	}
	flush()
	return out
}

// collapse makes a run of equal lines one line, dated at its last, with the
// count: a scheduled command fires the same line many times.
func collapse(lines []line) []line {
	var out []line
	for _, l := range lines {
		l.times = 1
		if n := len(out); n > 0 && l.kind != record.Note && !l.count && out[n-1].kind == l.kind && out[n-1].text == l.text {
			out[n-1].at = l.at
			out[n-1].times++
			continue
		}
		out = append(out, l)
	}
	return out
}

func join(head, tail string) string {
	if tail == "" {
		return head
	}
	return head + "  " + tail
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// title is the name a session goes by: its title, else its directory, else
// the start of its id.
func (v View) title(s Session) string {
	switch {
	case s.Record.Title != "":
		return s.Record.Title
	case s.Record.Cwd != "":
		return filepath.Base(s.Record.Cwd)
	}
	return "session " + short(s.Record.ID)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// unread is the line that reports a transcript problem, or "" for a
// transcript read in full.
func unread(rec record.Record) string {
	switch rec.Status {
	case record.Missing:
		return "no transcript"
	case record.Unreadable:
		return "transcript unreadable"
	case record.Partial:
		return plural(rec.UnreadLines, "line") + " could not be read"
	}
	return ""
}

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
			links = append(links, pull(pr))
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
		if len(s.Labels) > 0 {
			rows := make([]trow, 0, len(s.Labels))
			for _, l := range s.Labels {
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

// pull is a pull request in the header, newest first there: its number, how
// the session came by it, and its link.
func pull(e record.Event) string {
	how := "linked"
	if e.OpenedHere {
		how = "opened here"
	}
	return join(fmt.Sprintf("PR #%d %s", e.Number, how), e.URL)
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

// trow is a row that ends in free text, which is cut to the room the cells
// before it leave.
type trow struct {
	lead  []cell
	text  string
	style style
}

// table prints rows in columns behind an indent. A row's text is cut to the
// room left in the view's width, or to most columns when the width is not
// known.
func (v View) table(w io.Writer, indent string, rows []trow, most int) {
	leads, cols := make([][]cell, len(rows)), 0
	for i, r := range rows {
		leads[i] = r.lead
		cols = max(cols, len(r.lead))
	}
	room := most
	if v.Width > 0 {
		room = max(v.Width-width(indent)-starts(leads, viewGap)[cols], textFloor)
	}
	for i, r := range rows {
		for len(leads[i]) < cols {
			leads[i] = append(leads[i], cell{})
		}
		leads[i] = append(leads[i], of(r.style, cut(r.text, room)))
	}
	for _, line := range v.lay(leads, viewGap) {
		fmt.Fprintln(w, indent+line)
	}
}

// quiet are the kinds drawn back in the full history when no label lists
// them: they are most of its rows, and nothing counts them.
var quiet = []record.Kind{record.Skill, record.Read, record.Snippet, record.Compaction}

// timeline prints lines newest first: the labels each is under, its time,
// and its text.
func (v View) timeline(w io.Writer, lines []line) {
	rows := make([]trow, 0, len(lines))
	for _, l := range slices.Backward(lines) {
		if l.count {
			rows = append(rows, trow{lead: []cell{{}, {}}, text: l.text})
			continue
		}
		var tags cell
		for i, name := range l.labels {
			if i > 0 {
				tags = tags.add(plain, " ")
			}
			tags = tags.add(v.hue(name), name)
		}
		if l.kind == record.Note {
			tags = of(faint, "note")
		}
		row := trow{lead: []cell{tags, of(plain, v.brief(l.at))}, text: l.text, style: l.style}
		if l.times > 1 {
			row.text += fmt.Sprintf("  (%d times)", l.times)
		}
		if len(l.labels) == 0 && l.style == plain && slices.Contains(quiet, l.kind) {
			row.style = faint
		}
		rows = append(rows, row)
	}
	v.table(w, "  ", rows, textWidth)
}

// caveat says what the timeline may be missing: lines that did not decode,
// and facts a second trace saw that the reader's rule did not.
func (v View) caveat(rec record.Record) string {
	var parts []string
	if rec.Status == record.Partial {
		parts = append(parts, unread(rec))
	}
	if missed := rec.Missed(); len(missed) > 0 {
		var facts []string
		for _, s := range missed {
			facts = append(facts, fmt.Sprintf("%d × %s", s.Missed, s.Fact))
		}
		parts = append(parts, "the reader may have missed "+strings.Join(facts, ", ")+" (claude-steps check)")
	}
	return strings.Join(parts, "; ")
}

// loud is how a count of commits made since an event is drawn: strong when
// there are any. It is the same fact said louder, and never a colour of its
// own.
func loud(commits int) style {
	if commits > 0 {
		return strong
	}
	return plain
}

// labelCell is a label's board cell: when its latest event happened, with
// "+N" for the commits made since where the label counts them. It is one of
//
//	·   <time>   <time> +N   read <time>   read <time> +N   named <time>
//
// and nothing else: a cell carries a date, never a judgement.
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
	}
	c := of(plain, when)
	if n := l.CommitsSince; n != nil {
		c = c.add(plain, " ").add(loud(*n), fmt.Sprintf("+%d", *n))
	}
	return c
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
		if e.Outcome == "error" {
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

// boardRow is one session on the board before its title and note are cut to
// the room there is.
type boardRow struct {
	cells  []cell // every column but the note; cells[1] waits for the title
	title  string
	caveat string
	note   string
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
			for _, l := range s.Labels {
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
			if r.caveat = v.caveat(rec); r.caveat != "" {
				r.note = strings.TrimSpace("[" + r.caveat + "] " + r.note)
			}
		}
		for len(r.cells) < len(header) {
			r.cells = append(r.cells, cell{})
		}
		titleW = max(titleW, min(width(r.title)+r.mark().width(), titleWidth))
		noteW = max(noteW, width(r.note))
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
		mark := r.mark()
		r.cells[1] = mark.add(plain, clip(r.title, titleW-mark.width()))
		if noteW > 0 {
			r.cells = append(r.cells, r.noteCell(noteW))
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

// noteCell is the caveat and the latest note, cut to n columns.
func (r boardRow) noteCell(n int) cell {
	text := clip(r.note, n)
	if r.caveat == "" {
		return of(plain, text)
	}
	if rest, ok := strings.CutPrefix(text, "["+r.caveat+"]"); ok {
		return of(problem, "["+r.caveat+"]").add(plain, rest)
	}
	return of(problem, text)
}

// sessionJSON is the machine form of a session. Times are RFC 3339 and
// nothing is relative, so the output does not depend on when it was made.
type sessionJSON struct {
	Pane  string `json:"pane,omitempty"`
	Where string `json:"where,omitempty"`
	record.Record
	PullRequests []record.Event      `json:"pull_requests"`
	Compactions  []record.Event      `json:"compactions"`
	Labels       []record.LabelState `json:"labels"`
}

func toJSON(s Session) sessionJSON {
	out := sessionJSON{Record: s.Record, PullRequests: s.Record.PullRequests(), Compactions: s.Record.Compactions(), Labels: s.Labels}
	if s.Pane != nil {
		out.Pane, out.Where = s.Pane.ID, s.Pane.Where
	}
	if out.PullRequests == nil {
		out.PullRequests = []record.Event{}
	}
	if out.Compactions == nil {
		out.Compactions = []record.Event{}
	}
	if out.Labels == nil {
		out.Labels = []record.LabelState{}
	}
	return out
}

func ShowJSON(w io.Writer, s Session) error {
	return encode(w, toJSON(s))
}

func BoardJSON(w io.Writer, sessions []Session) error {
	out := make([]sessionJSON, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, toJSON(s))
	}
	return encode(w, out)
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
