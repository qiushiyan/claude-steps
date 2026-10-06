// Package render turns records into the text and JSON the command prints.
// Its functions are pure: the current time, the home directory, the width to
// fill and whether to colour come in through View.
//
// Nothing here says a check is finished or still holds. A line states what the
// transcript holds and when it happened, and colour follows the same rule: a
// hue names a label or marks a problem, and never grades a date.
package render

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/panes"
	"github.com/qiushiyan/claude-steps/internal/record"
)

// Session is one session as a view shows it: the record, and the pane it runs
// in when it is live. Its state under each label is the view's to work out,
// from the labels it was given.
type Session struct {
	Pane   *panes.Pane
	Record record.Record
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
	textFloor  = 24 // the least room the timeline gives an event's text; the popup wraps what runs past
	ruleWidth  = 72 // the rule under the header when the width is not known
	shortBelow = 80 // a head narrower than this gives a row's time as a cell does: "8m", "+1"
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
