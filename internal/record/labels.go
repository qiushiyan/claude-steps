package record

import (
	"slices"
	"strings"

	"github.com/qiushiyan/claude-steps/internal/config"
)

// LabelState is what a label's column shows for one session: its latest
// event and, where the label asks, the commits made since. It holds no
// verdict; whether that event still covers the code is the reader's call.
type LabelState struct {
	Name string `json:"name"`
	// Latest is nil when the label has no step in the session.
	Latest *Event `json:"latest,omitempty"`
	// CommitsSince counts the commits made after Latest. It is set when the
	// label asks for it and Latest is more than a mention.
	CommitsSince *int `json:"commits_since,omitempty"`
}

// Summarise reduces a session's events to one state per label, read from
// the session's steps, so a label's row says what its steps say.
//
// A label's latest event is its latest step that did not fail: a run, a
// read that answered a request, a round, or a request nothing answered. A
// round whose run returned an error and that was collected all the same did
// not fail: a result came back. When there is none it is the latest prompt
// that named one of its skills. Commits are counted from that event's time,
// which for a round is its dispatch: a commit made while a review runs is not
// covered by it.
func Summarise(events []Event, labels []config.Label) []LabelState {
	steps := Steps(events, labels)
	out := make([]LabelState, 0, len(labels))
	for _, l := range labels {
		var ran, named *Event
		for _, s := range steps {
			if !slices.Contains(s.Labels, l.Name) {
				continue
			}
			switch e := &events[s.Index]; {
			case e.Kind == Mention:
				named = e
			case !e.Failed || e.CollectedAt != nil:
				ran = e
			}
		}
		st := LabelState{Name: l.Name, Latest: named}
		if ran != nil {
			st.Latest = ran
		}
		if st.Latest != nil && st.Latest.Kind != Mention && l.CountCommits {
			n := 0
			for _, e := range events {
				if e.Kind == Commit && e.At.After(st.Latest.At) {
					n++
				}
			}
			st.CommitsSince = &n
		}
		out = append(out, st)
	}
	return out
}

// LabelsOf names the labels an event belongs to, in configuration order.
// Labels are not exclusive: two may list one skill, and a prompt may name
// skills of several. A call that failed still belongs, so its failure is read
// under its label.
func LabelsOf(labels []config.Label, e Event) []string {
	var names []string
	for _, l := range labels {
		if belongs(l, &e) {
			names = append(names, l.Name)
		}
	}
	return names
}

// belongs is the one rule for which label an event is under. Steps and
// LabelsOf both read it.
func belongs(l config.Label, e *Event) bool {
	switch e.Kind {
	case Skill, Read:
		return slices.Contains(l.Skills, bareSkill(e.Name))
	case Snippet:
		// A paste that arrived as a slash command is that command's run under
		// a label that lists the command's skill: one prompt, one line there.
		ran := e.Command != "" && slices.Contains(l.Skills, bareSkill(e.Command))
		return slices.Contains(l.Snippets, e.Name) && !ran
	case Mention:
		// Typed as a command's arguments, the words ask for other skills than
		// the command's own: under a label that lists it, the run is the request.
		if e.Command != "" && slices.Contains(l.Skills, bareSkill(e.Command)) {
			return false
		}
		return slices.ContainsFunc(e.Names, func(n string) bool { return slices.Contains(l.Skills, n) })
	case Round:
		return slices.ContainsFunc(l.Jobs, func(prefix string) bool { return strings.HasPrefix(e.Name, prefix) })
	}
	return false
}

// bareSkill drops a plugin namespace: "plugin:review" is the skill "review".
func bareSkill(name string) string {
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}
