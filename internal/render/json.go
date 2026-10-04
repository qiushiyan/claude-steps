package render

import (
	"encoding/json"
	"io"

	"github.com/qiushiyan/claude-steps/internal/record"
)

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

func (v View) toJSON(s Session) sessionJSON {
	rec := s.Record
	out := sessionJSON{Record: rec, PullRequests: rec.PullRequests(), Compactions: rec.Compactions(), Labels: record.Summarise(rec.Events, v.Labels)}
	if s.Pane != nil {
		out.Pane, out.Where = s.Pane.ID, s.Pane.Where
	}
	if out.PullRequests == nil {
		out.PullRequests = []record.Event{}
	}
	if out.Compactions == nil {
		out.Compactions = []record.Event{}
	}
	return out
}

func (v View) ShowJSON(w io.Writer, s Session) error {
	return encode(w, v.toJSON(s))
}

func (v View) BoardJSON(w io.Writer, sessions []Session) error {
	out := make([]sessionJSON, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, v.toJSON(s))
	}
	return encode(w, out)
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
