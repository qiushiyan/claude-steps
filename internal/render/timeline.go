package render

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/record"
)

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
		return join(linked(e), e.Repo)
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
func collect(e record.Event, when string) string {
	switch {
	case e.CollectFailed:
		return join("collect returned an error", when)
	case said(e) != "":
		return join("collected", when) + ", " + said(e)
	}
	return join("collected", when)
}

// said is envoy's word for a collected job, when it has one worth printing.
// Its "ok" says the job returned a result, not that a review passed, so it
// is not printed. Any other word is envoy's own.
func said(e record.Event) string {
	if e.Outcome == "" || e.Outcome == "ok" {
		return ""
	}
	return "envoy said " + e.Outcome
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
	// ask is the request the step's prompt made under its label, said before
	// what ran.
	ask *record.Event
}

// lineOf is the line an event starts as: its time, its labels and its words,
// drawn as a problem when the transcript says the call failed.
func (v View) lineOf(e record.Event) line {
	l := line{at: e.At, kind: e.Kind, labels: record.LabelsOf(v.Labels, e), text: v.describe(e)}
	if e.Failed {
		l.style = problem
	}
	return l
}

// round is a round's words among the steps and in its label's row: its name,
// and what went wrong where something did. How it was dispatched and
// collected is the history's to say.
func round(e record.Event) (string, style) {
	var words []string
	if e.Failed {
		words = append(words, dispatch(e))
	}
	switch {
	case e.CollectFailed:
		words = append(words, "collect returned an error")
	case said(e) != "":
		words = append(words, said(e))
	}
	st := plain
	if e.Failed || e.CollectFailed {
		st = problem
	}
	return join(e.Name, strings.Join(words, ", ")), st
}

// outline is the timeline the steps are picked from, oldest first: the
// session's steps as record.Steps reads them, each with its prompt's request
// or the run whose words a round carries, its commits, and its notes. The full history keeps every event at its own time.
func (v View) outline(rec record.Record) []line {
	steps := map[int]record.Step{}
	for _, st := range record.Steps(rec.Events, v.Labels) {
		steps[st.Index] = st
	}
	var out []line
	for i, e := range rec.Events {
		st, ok := steps[i]
		switch {
		case ok:
			l := v.lineOf(e)
			l.labels, l.ask = st.Labels, st.Ask
			if e.Kind == record.Round {
				l.text, l.style = round(e)
			}
			if st.Lent != nil {
				l.text = join(l.text, v.describe(*st.Lent))
			}
			out = append(out, l)
		case e.Kind == record.Commit:
			out = append(out, v.lineOf(e))
		}
	}
	for _, n := range rec.Notes {
		out = append(out, v.lineOf(record.Event{At: n.At, Kind: record.Note, Text: n.Text}))
	}
	slices.SortStableFunc(out, func(a, b line) int { return a.at.Compare(b.at) })
	return out
}

// lines are a session's whole timeline, oldest first. A round this session
// dispatched and collected is two lines, each at its own time.
func (v View) lines(rec record.Record) []line {
	var out []line
	for _, e := range rec.Timeline() {
		l := v.lineOf(e)
		if e.Kind == record.Round && e.CollectedAt != nil {
			got := l
			got.at, got.style = *e.CollectedAt, plain
			if e.CollectFailed {
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
// count: a scheduled command fires the same line many times. Lines are equal
// when their labels are too: the same words under another label are another
// step.
func collapse(lines []line) []line {
	var out []line
	for _, l := range lines {
		l.times = 1
		if n := len(out); n > 0 && l.kind != record.Note && !l.count && out[n-1].kind == l.kind && out[n-1].text == l.text && sameAsk(out[n-1].ask, l.ask) && slices.Equal(out[n-1].labels, l.labels) {
			out[n-1].at = l.at
			out[n-1].times++
			continue
		}
		out = append(out, l)
	}
	return out
}

// request is the words of a step's request in at most n columns, or "" when
// they do not fit: a paste's key, the opening of the prompt, or the slash
// command as it was typed.
func (v View) request(e record.Event, n int) string {
	switch {
	case e.Kind == record.Mention:
		// Quoting escapes some characters, so the words are cut until the
		// quoted form fits, closing quote and all; with fewer than four
		// columns of words the request is left out.
		for m := n - 7; m >= 4; m-- {
			if q := fmt.Sprintf("you: %q", cut(oneLine(e.Text), m)); width(q) <= n {
				return q
			}
		}
		return ""
	case e.Kind == record.Skill:
		return cut(oneLine("/"+e.Name+" "+e.Args), n)
	}
	return cut(oneLine(v.describe(e)), n)
}

// sameAsk reports whether two lines say the same request.
func sameAsk(a, b *record.Event) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Name == b.Name && a.Text == b.Text && a.Args == b.Args && a.At.Equal(b.At)
}

// linked names a pull request and says how the session came by it. The
// header follows it with the link, the history with the repository.
func linked(e record.Event) string {
	if e.OpenedHere {
		return fmt.Sprintf("PR #%d opened here", e.Number)
	}
	return fmt.Sprintf("PR #%d linked", e.Number)
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
		// A note is folded rather than cut: it is the user's words.
		row := trow{lead: []cell{tags, of(plain, v.brief(l.at))}, text: l.text, style: l.style, fold: l.kind == record.Note}
		if ask := l.ask; ask != nil {
			row.ask = func(n int) string { return v.request(*ask, n) }
		}
		if l.times > 1 {
			row.text += fmt.Sprintf("  (%d times)", l.times)
		}
		if len(l.labels) == 0 && l.style == plain && slices.Contains(quiet, l.kind) {
			row.style = faint
		}
		rows = append(rows, row)
	}
	v.table(w, "  ", rows, textWidth, textFloor)
}
