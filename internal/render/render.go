// Package render turns records into the text and JSON the command prints.
// Its functions are pure: the current time and the home directory come in
// through View.
//
// Nothing here says a check is finished or still holds. A line states what the
// transcript holds and when it happened.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"

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
}

const (
	titleWidth = 34
	labelWidth = 70
	noteWidth  = 48
	textWidth  = 96
	nothing    = "·"
)

func (v View) ago(at time.Time) string {
	if at.IsZero() {
		return "at an unknown time"
	}
	return humanize.RelTime(at, v.Now, "ago", "from now")
}

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
		// envoy's "ok" says the job returned a result, not that a review
		// passed, so it is not printed. Any other word is envoy's own.
		switch {
		case e.CollectedAt != nil && e.Outcome == "error":
			return e.Name + "  collect returned an error"
		case e.CollectedAt != nil && e.Outcome != "" && e.Outcome != "ok":
			return e.Name + "  collected, envoy said " + e.Outcome
		case e.CollectedAt != nil:
			return e.Name + "  collected"
		case e.Failed:
			return e.Name + "  run returned an error"
		}
		return e.Name + "  dispatched"
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
		return "note: " + oneLine(e.Text)
	}
	return string(e.Kind)
}

func join(head, tail string) string {
	if tail == "" {
		return head
	}
	return head + "  " + tail
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	r := []rune(oneLine(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
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

// Show prints one session: its identity, each label's latest event, the
// timeline oldest first, and the notes.
func (v View) Show(w io.Writer, s Session) {
	rec := s.Record
	head := []string{v.title(s), short(rec.ID)}
	if s.Pane != nil {
		head = append(head, s.Pane.Where)
	}
	fmt.Fprintln(w, strings.Join(head, "   "))
	if rec.Cwd != "" {
		fmt.Fprintln(w, join(v.path(rec.Cwd), rec.Branch))
	}

	readable := rec.Status == record.OK || rec.Status == record.Partial
	if !readable {
		fmt.Fprintln(w)
		fmt.Fprintln(w, join(unread(rec), v.path(rec.Path)))
	} else {
		var facts []string
		for _, pr := range rec.PullRequests() {
			facts = append(facts, v.describe(pr))
		}
		if cs := rec.Compactions(); len(cs) > 0 {
			facts = append(facts, plural(len(cs), "compaction")+", last "+v.ago(cs[len(cs)-1].At))
		}
		if len(facts) > 0 {
			fmt.Fprintln(w, strings.Join(facts, "   "))
		}
		if len(s.Labels) > 0 {
			fmt.Fprintln(w)
			var rows [][]string
			for _, l := range s.Labels {
				rows = append(rows, v.labelRow(l))
			}
			table(w, "", rows)
		}

		fmt.Fprintln(w)
		timeline := rec.Timeline()
		if len(timeline) == 0 {
			fmt.Fprintln(w, "no events in the transcript")
		}
		// A scheduled command fires the same line many times; a run of equal
		// lines is one line, dated at its last, with the count.
		var rows [][]string
		last, run := "", 0
		for _, e := range timeline {
			text := v.describe(e)
			if e.Kind == record.Round {
				text = "  " + text
			}
			if text == last && e.Kind != record.Note {
				run++
				rows[len(rows)-1] = []string{v.ago(e.At), fmt.Sprintf("%s  (%d times)", text, run)}
				continue
			}
			last, run = text, 1
			rows = append(rows, []string{v.ago(e.At), text})
		}
		table(w, "  ", rows)
		if note := v.caveat(rec); note != "" {
			fmt.Fprintln(w)
			fmt.Fprintln(w, note)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "notes")
	if len(rec.Notes) == 0 && rec.NotesError == "" {
		fmt.Fprintln(w, "  none")
	}
	var rows [][]string
	for _, n := range rec.Notes {
		rows = append(rows, []string{v.ago(n.At), oneLine(n.Text)})
	}
	table(w, "  ", rows)
	if rec.UnreadNotes > 0 {
		fmt.Fprintln(w, "  "+plural(rec.UnreadNotes, "note line")+" could not be read")
	}
	if rec.NotesError != "" {
		fmt.Fprintln(w, "  "+rec.NotesError)
	}
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

// labelCell is a label's board cell: when its latest event happened, with
// "+N" for the commits made since where the label counts them. It is one of
//
//	·   <time>   <time> +N   read <time>   read <time> +N   named <time>
//
// and nothing else: a cell carries a date, never a judgement.
func (v View) labelCell(l record.LabelState) string {
	if l.Latest == nil {
		return nothing
	}
	cell := v.ago(l.Latest.At)
	switch l.Latest.Kind {
	case record.Mention:
		return "named " + cell
	case record.Read:
		cell = "read " + cell
	}
	if l.CommitsSince != nil {
		cell += fmt.Sprintf(" +%d", *l.CommitsSince)
	}
	return cell
}

// labelRow is a label's line in a session view: its name, when its latest
// event happened, the commits since, and the event itself.
func (v View) labelRow(l record.LabelState) []string {
	if l.Latest == nil {
		return []string{l.Name, nothing}
	}
	since := ""
	if l.CommitsSince != nil {
		since = plural(*l.CommitsSince, "commit") + " since"
	}
	return []string{l.Name, v.ago(l.Latest.At), since, clip(v.describe(*l.Latest), labelWidth)}
}

// Board prints one row per session. With ids, every line starts with the
// pane id, a tab, the session id and a tab, both empty on the header line. A
// picker hides the two fields and acts on them, so a preview or a note goes
// to the session the row showed, whatever the pane runs by then.
func (v View) Board(w io.Writer, sessions []Session, ids bool) {
	header := []string{"pane", "session"}
	if len(sessions) > 0 {
		for _, l := range sessions[0].Labels {
			header = append(header, l.Name)
		}
	}
	header = append(header, "PR", "compactions", "note")
	rows := [][]string{header}
	for _, s := range sessions {
		rec := s.Record
		row := []string{"", clip(v.title(s), titleWidth)}
		if s.Pane != nil {
			row[0] = s.Pane.Where
		}
		note := ""
		if n := len(rec.Notes); n > 0 {
			note = clip(rec.Notes[n-1].Text, noteWidth)
			if n > 1 {
				note = fmt.Sprintf("(%d) %s", n, note)
			}
		}
		if rec.Status == record.Missing || rec.Status == record.Unreadable {
			row = append(row, unread(rec))
			for len(row) < len(header)-1 {
				row = append(row, "")
			}
			rows = append(rows, append(row, note))
			continue
		}
		for _, l := range s.Labels {
			row = append(row, v.labelCell(l))
		}
		var prs []string
		for _, pr := range rec.PullRequests() {
			prs = append(prs, fmt.Sprintf("#%d", pr.Number))
		}
		if len(prs) == 0 {
			prs = []string{nothing}
		}
		if caveat := v.caveat(rec); caveat != "" {
			note = strings.TrimSpace("[" + caveat + "] " + note)
		}
		row = append(row, strings.Join(prs, " "), fmt.Sprint(len(rec.Compactions())), note)
		rows = append(rows, row)
	}

	lines := align(rows)
	for i, line := range lines {
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

// table prints rows in aligned columns behind an indent.
func table(w io.Writer, indent string, rows [][]string) {
	for _, line := range align(rows) {
		fmt.Fprintln(w, indent+line)
	}
}

func align(rows [][]string) []string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+3))
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return lines
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
