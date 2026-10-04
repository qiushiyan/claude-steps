// Package record owns everything known about one session: where its
// transcript is, what the transcript holds, and the user's notes. Every view
// gets a session's Record from Loader.Load, and nothing else opens a
// transcript.
package record

import (
	"slices"
	"time"

	"github.com/qiushiyan/claude-steps/internal/notes"
)

// Status says how much of the transcript was read. Only OK and Partial carry
// events.
type Status string

const (
	OK         Status = "ok"
	Partial    Status = "partial"    // read, with lines that could not be decoded
	Unreadable Status = "unreadable" // cannot be opened, or holds no row the reader knows
	Missing    Status = "missing"    // no transcript file for the session id
)

type Kind string

const (
	Skill      Kind = "skill"
	Read       Kind = "read"
	Snippet    Kind = "snippet"
	Mention    Kind = "mention"
	Round      Kind = "round"
	Commit     Kind = "commit"
	PR         Kind = "pr"
	Compaction Kind = "compaction"
	Note       Kind = "note"
)

// Event is one dated fact. Which fields are set depends on Kind.
type Event struct {
	At   time.Time `json:"at"`
	Kind Kind      `json:"kind"`

	// Name is the skill as it was invoked or read, the snippet's key, or the
	// envoy job's name.
	Name string `json:"name,omitempty"`
	// Args are the words that followed a skill invocation.
	Args string `json:"args,omitempty"`
	// Via tells a slash command the user typed ("slash") from a Skill call
	// the model made ("tool").
	Via string `json:"via,omitempty"`
	// Failed: the skill call, or the envoy run call, returned an error.
	Failed bool `json:"failed,omitempty"`

	// Names are the labelled skills a mention's prompt names.
	Names []string `json:"names,omitempty"`
	// Text is a mention's opening words, a commit's subject, or a note.
	Text string `json:"text,omitempty"`

	Amend bool `json:"amend,omitempty"`
	// Dir is where a commit was made when that is not the session's own
	// directory.
	Dir string `json:"dir,omitempty"`

	// A round was Dispatched when this session ran `envoy run` for it. At is
	// then the dispatch; otherwise it is the first collect.
	Dispatched  bool       `json:"dispatched,omitempty"`
	CollectedAt *time.Time `json:"collected_at,omitempty"`
	// Outcome is the first word of the collect's status line, as envoy
	// printed it, or "error" when the collect call itself failed.
	Outcome string `json:"outcome,omitempty"`

	Repo   string `json:"repo,omitempty"`
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
	// OpenedHere: a `gh pr create` call in this session returned the URL.
	OpenedHere bool `json:"opened_here,omitempty"`

	Trigger string `json:"trigger,omitempty"`
}

// Signal counts one fact two ways across a transcript: by the rows the reader
// lifts it from, and by a second trace that should exist whenever the first
// does. Missed counts second traces with no matching first; above zero it
// means the format moved under the reader.
type Signal struct {
	Fact    string `json:"fact"`
	Primary int    `json:"primary"`
	Second  int    `json:"second"`
	Missed  int    `json:"missed"`
	// Filled counts the misses the reader shows anyway, from the second
	// trace: the view has nothing to warn about for them.
	Filled int `json:"filled,omitempty"`
}

// Record is one session as the views show it.
type Record struct {
	ID     string    `json:"id"`
	Path   string    `json:"path,omitempty"`
	Title  string    `json:"title,omitempty"`
	Cwd    string    `json:"cwd,omitempty"`
	Branch string    `json:"branch,omitempty"`
	LastAt time.Time `json:"last_at,omitzero"`

	Status Status `json:"status"`
	// UnreadLines counts transcript lines that could not be decoded.
	UnreadLines int `json:"unread_lines"`

	// Events are the transcript's facts in time order. Notes are kept apart
	// because they come from the store; Timeline merges the two.
	Events []Event      `json:"events"`
	Notes  []notes.Note `json:"notes"`
	// UnreadNotes counts lines of the notes file that could not be read;
	// NotesError is set when the file itself could not be.
	UnreadNotes int    `json:"unread_notes"`
	NotesError  string `json:"notes_error,omitempty"`

	// Signals count each fact two ways; `check` sums them, and a view says so
	// when a second trace saw what the reader's rule missed.
	Signals []Signal `json:"-"`
	// Turns counts the conversation rows read, sidechains included.
	Turns int `json:"-"`
}

// Missed returns the facts the timeline may lack: a second trace saw them,
// the reader's rule did not, and the reader could not show them anyway.
func (r Record) Missed() []Signal {
	var out []Signal
	for _, s := range r.Signals {
		if s.Missed > s.Filled {
			s.Missed -= s.Filled
			out = append(out, s)
		}
	}
	return out
}

// Timeline returns the events and the notes as one list in time order.
func (r Record) Timeline() []Event {
	out := slices.Clone(r.Events)
	for _, n := range r.Notes {
		out = append(out, Event{At: n.At, Kind: Note, Text: n.Text})
	}
	slices.SortStableFunc(out, func(a, b Event) int { return a.At.Compare(b.At) })
	return out
}

func (r Record) of(kind Kind) []Event {
	var out []Event
	for _, e := range r.Events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// PullRequests returns every pull request the session linked, each once, at
// the time it was first linked.
func (r Record) PullRequests() []Event { return r.of(PR) }

// Compactions returns the compactions of the main conversation.
func (r Record) Compactions() []Event { return r.of(Compaction) }

// Uncollected returns the rounds this session dispatched for which the
// transcript holds no collect, oldest first. It cannot tell a round still
// running from one collected in another session or given up on.
func (r Record) Uncollected() []Event {
	var out []Event
	for _, e := range r.Events {
		if e.Kind == Round && e.Dispatched && !e.Failed && e.CollectedAt == nil {
			out = append(out, e)
		}
	}
	return out
}
