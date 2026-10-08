package record

import (
	"slices"

	"github.com/qiushiyan/claude-steps/internal/config"
)

// Step is one thing that happened under a set of labels: a run, a read or a
// round, said with the request that asked for it, or a request nothing
// answered. A session's steps and each label's row are both read from the
// steps, so the two cannot disagree about what happened under a label.
type Step struct {
	// Index is the event's place among the events the steps were read from.
	Index  int
	Event  Event
	Labels []string
	// Ask is the request the step's prompt made under its label: a paste, a
	// prompt that names a skill, or a slash command the user typed. A run or
	// a dispatched round with none is one the reader found no request for; a
	// failed call and a round collected from elsewhere carry none.
	Ask *Event
	// Lent is the run of the model's own that a round took, whose words the
	// round carries when no request is said.
	Lent *Event
}

// Steps reads what happened under each label, oldest first. A request under
// a label is answered by the first run, read or round under that label made
// in the same human prompt, or by the slash command typed in the next one,
// which is a prompt of its own; it is then that step's request and no step
// there. It is said too on every later run and dispatched round under the
// label in the answering step's prompt, and a slash command typed with
// nothing to answer is its own prompt's request the same way, so such a step
// with no request said is one the reader found no request for. A failed call
// and a round collected from elsewhere answer nothing and say nothing. The
// same request sent again before an answer is one. Where no prompt is known to be the user's, nothing says
// which run answered which request, and they stay apart.
//
// A read is a step only as the answer to a request: asked in prose to run a
// skill, the model often prints its file and loads nothing, but it reads a
// skill's file as often to look something up, and a lookup is no stage.
//
// Under a label that lists rounds a step is a round: every round a skill
// there runs is dispatched, so the label's runs and reads since its last
// round are this round's and no steps there. The round's request is the
// latest of theirs, a slash command among them, or one made in the round's
// own prompt; with none, the latest run lends the round the model's words.
// That join is by order alone. A dispatch the session replaced is no step.
//
// Each label is read on its own, so a run under two labels that a round of
// one takes is still a step under the other.
func Steps(events []Event, labels []config.Label) []Step {
	var merged []step
	byIndex := map[int]int{} // event index → its place in merged
	for _, l := range labels {
		steps, _ := labelSteps(events, l)
		for _, s := range steps {
			at, ok := byIndex[s.index]
			if !ok {
				s.labels = []string{l.Name}
				byIndex[s.index] = len(merged)
				merged = append(merged, s)
				continue
			}
			m := &merged[at]
			m.labels = append(m.labels, l.Name)
			m.ask, m.lent = max(m.ask, s.ask), max(m.lent, s.lent)
		}
	}
	slices.SortFunc(merged, func(a, b step) int { return a.index - b.index })
	out := make([]Step, len(merged))
	for i, s := range merged {
		out[i] = Step{Index: s.index, Event: events[s.index], Labels: s.labels}
		if s.ask >= 0 {
			out[i].Ask = &events[s.ask]
		}
		// A request under any of the step's labels is said, and then the
		// model's words are not.
		if s.lent >= 0 && s.ask < 0 {
			out[i].Lent = &events[s.lent]
		}
	}
	return out
}

// step is a Step as it is read, its events by their place: -1 is none.
type step struct {
	index, ask, lent int
	labels           []string
}

// labelSteps are the steps under one label, each without its labels, and
// the reads among them, which a round may since have taken.
func labelSteps(events []Event, l config.Label) (steps []step, reads []int) {
	var out []step
	ask := -1              // a request nothing under the label has answered yet
	said, saidIn := -1, 0  // the request said on the runs and rounds of prompt saidIn
	var loads []int        // the places in out of the runs and reads since the label's last round
	gone := map[int]bool{} // events a later step took
	add := func(i int) *step {
		out = append(out, step{index: i, ask: -1, lent: -1})
		return &out[len(out)-1]
	}
	// answer is the request a run, read or round came under: the one waiting,
	// which it answers and which its prompt then says, or the one its prompt
	// already says. first reports the first.
	answer := func(e Event) (request int, first bool) {
		if ask >= 0 && answers(events[ask], e) {
			request = ask
			gone[ask], ask = true, -1
			said, saidIn = request, e.Prompt
			return request, true
		}
		if said >= 0 && e.Prompt == saidIn {
			return said, false
		}
		return -1, false
	}
	for i := range events {
		e := events[i]
		if !belongs(l, &e) {
			continue
		}
		switch {
		case e.Kind == Round && e.Redispatched:
		case e.Kind == Snippet || e.Kind == Mention:
			if ask >= 0 && sameRequest(events[ask], e) {
				gone[ask] = true
			}
			ask = i
			add(i)
		case e.Kind == Round && e.Dispatched && !e.Failed:
			took := make([]step, 0, len(loads))
			for _, at := range loads {
				took = append(took, out[at])
			}
			loads = nil
			s := add(i)
			for _, load := range took {
				gone[load.index] = true
				switch run := events[load.index]; {
				case load.ask >= 0:
					s.ask = max(s.ask, load.ask)
				case run.Kind == Skill && run.Via == "slash":
					s.ask = max(s.ask, load.index)
				case run.Kind == Skill:
					s.lent = load.index
				}
			}
			if request, _ := answer(e); request >= 0 {
				s.ask = max(s.ask, request)
			}
			if s.ask >= 0 {
				s.lent = -1
			}
		case e.Kind == Skill && !e.Failed:
			s := add(i)
			request, first := answer(e)
			switch {
			case first || e.Via != "slash":
				s.ask = request
			case e.Prompt > 0:
				// Typed with nothing to answer, the command is its prompt's request.
				said, saidIn = i, e.Prompt
			}
			loads = append(loads, len(out)-1)
		case e.Kind == Read:
			if request, first := answer(e); first {
				add(i).ask = request
				loads = append(loads, len(out)-1)
				reads = append(reads, i)
			}
		default:
			add(i)
		}
	}
	kept := out[:0]
	for _, s := range out {
		if !gone[s.index] {
			kept = append(kept, s)
		}
	}
	return kept, reads
}

// ReadsLeftOut counts the reads of a labelled skill's file that only the
// full history shows: each answered a request under none of its labels, so
// it is no step and no round took it.
func ReadsLeftOut(events []Event, labels []config.Label) int {
	stepped := map[int]bool{}
	for _, l := range labels {
		_, reads := labelSteps(events, l)
		for _, i := range reads {
			stepped[i] = true
		}
	}
	n := 0
	for i, e := range events {
		if e.Kind == Read && !stepped[i] && len(LabelsOf(labels, e)) > 0 {
			n++
		}
	}
	return n
}

// answers reports whether a run or round came in answer to a request: under
// the same human prompt, or as the slash command typed next. A request no
// prompt is known for is answered by nothing.
func answers(request, e Event) bool {
	if request.Prompt == 0 {
		return false
	}
	slash := e.Kind == Skill && e.Via == "slash"
	return e.Prompt == request.Prompt || slash && e.Prompt == request.Prompt+1
}

// sameRequest reports whether two requests ask the same thing: a paste of
// the same snippet, or the same words.
func sameRequest(a, b Event) bool {
	return a.Kind == b.Kind && a.Name == b.Name && a.Text == b.Text
}
