package render

import (
	"fmt"
	"io"
	"slices"
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
	// ask is the request a step answered, said before what ran.
	ask string
	// short is a request's words when another line says them: cut to
	// askWidth, so what ran keeps its room.
	short  string
	prompt int  // the human prompt the event came under
	slash  bool // a slash command the user typed, which is a prompt of its own
}

// lineOf is the line an event starts as: its time, its labels and its words,
// drawn as a problem when the transcript says the call failed.
func (v View) lineOf(e record.Event) line {
	l := line{at: e.At, kind: e.Kind, labels: record.LabelsOf(v.Labels, e), text: v.describe(e),
		prompt: e.Prompt, slash: e.Kind == record.Skill && e.Via == "slash"}
	switch {
	case e.Kind == record.Mention:
		l.short = fmt.Sprintf("you: %q", cut(oneLine(e.Text), askWidth-7))
	case e.Kind == record.Snippet, l.slash:
		l.short = cut(oneLine(l.text), askWidth)
	}
	if e.Failed {
		l.style = problem
	}
	return l
}

// round is a round's words among the steps and in its label's row: its name,
// and what went wrong where something did. How it was dispatched and
// collected is the history's to say.
func round(e record.Event) (string, style) {
	switch {
	case e.Failed && e.CollectedAt != nil:
		return join(e.Name, dispatch(e)+", "+collect(e, "")), problem
	case e.Failed:
		return join(e.Name, dispatch(e)), problem
	case e.CollectFailed:
		return join(e.Name, collect(e, "")), problem
	}
	return join(e.Name, said(e)), plain
}

// outline is the timeline the steps are picked from, oldest first: a line
// for each thing that ran under a label, and for each request nothing
// answered. A request under a label (a paste, a prompt that names a skill) is
// answered by the first run or round under that label in the same human
// prompt, or by the slash command typed next, which is a prompt of its own:
// the answer's line says the request first, and the request has no line of
// its own there. The same request sent again before an answer is one.
//
// Under a label that lists rounds a step is a round, dated at its dispatch:
// every round a skill there runs is dispatched, so the skill's runs and reads
// since the label's last round are this round's, and their lines go. The
// latest request among them is the round's, a slash command the user typed
// being one; with none, the latest run lends the round the model's words. The
// join is by order alone. A dispatch the session replaced has no line. The
// full history keeps each of these at its own time.
func (v View) outline(rec record.Record) []line {
	var out []line
	loads := map[string][]int{} // label → the lines of its runs and reads since its last round
	asks := map[string]int{}    // label → the line of a request nothing under it has answered
	gone := map[int]bool{}      // lines another line took
	// settle takes a label off a request's line: it is answered there, or
	// sent again. A request with no label left has no line.
	settle := func(i int, name string) {
		out[i].labels = slices.DeleteFunc(slices.Clone(out[i].labels), func(n string) bool { return n == name })
		if len(out[i].labels) == 0 {
			gone[i] = true
		}
		delete(asks, name)
	}
	answer := func(l *line) {
		for _, name := range slices.Clone(l.labels) {
			i, ok := asks[name]
			if !ok || !(l.prompt == out[i].prompt || l.slash && l.prompt == out[i].prompt+1) {
				continue
			}
			l.ask = out[i].short
			settle(i, name)
		}
	}
	// prepare gives a round the runs and reads before it under its labels.
	prepare := func(l *line) {
		var took []int
		for _, name := range l.labels {
			took = append(took, loads[name]...)
			delete(loads, name)
		}
		slices.Sort(took)
		words := ""
		for _, i := range slices.Compact(took) {
			if gone[i] {
				continue
			}
			gone[i] = true
			switch r := out[i]; {
			case r.ask != "":
				l.ask = r.ask
			case r.slash:
				l.ask = r.short
			case r.kind == record.Skill:
				words = r.text
			}
		}
		if l.ask == "" {
			l.text = join(l.text, words)
		}
	}
	for _, e := range rec.Timeline() {
		l := v.lineOf(e)
		switch {
		case e.Kind == record.Round && e.Redispatched:
			continue
		case e.Kind == record.Snippet || e.Kind == record.Mention:
			for _, name := range l.labels {
				if i, ok := asks[name]; ok && out[i].text == l.text {
					settle(i, name)
				}
				asks[name] = len(out)
			}
		case e.Kind == record.Round:
			l.text, l.style = round(e)
			if e.Dispatched && !e.Failed {
				prepare(&l)
				if l.ask == "" {
					answer(&l)
				}
			}
		case e.Kind == record.Skill && !e.Failed, e.Kind == record.Read:
			answer(&l)
			for _, name := range l.labels {
				loads[name] = append(loads[name], len(out))
			}
		}
		out = append(out, l)
	}
	kept := out[:0]
	for i, l := range out {
		if !gone[i] {
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
		if n := len(out); n > 0 && l.kind != record.Note && !l.count && out[n-1].kind == l.kind && out[n-1].text == l.text && out[n-1].ask == l.ask && slices.Equal(out[n-1].labels, l.labels) {
			out[n-1].at = l.at
			out[n-1].times++
			continue
		}
		out = append(out, l)
	}
	return out
}

// asked puts a step's request before what ran in answer to it.
func asked(ask, text string) string {
	if ask == "" {
		return text
	}
	return ask + " → " + text
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
		row := trow{lead: []cell{tags, of(plain, v.brief(l.at))}, text: asked(l.ask, l.text), style: l.style, fold: l.kind == record.Note}
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
