package record

import (
	"fmt"
	"sync"
	"time"
)

const (
	// SurveyWindow is how far back a survey reads.
	SurveyWindow = 7 * 24 * time.Hour
	// MissTolerance: a survey reports drift in a fact when more than one in
	// this many of its second traces has no match in the reader's own count.
	MissTolerance = 10
)

// Survey is every transcript changed in the last week, read through Load as
// the views read it, with each fact counted two ways.
type Survey struct {
	Window      time.Duration
	Transcripts int
	Partial     int      // transcripts read in part
	UnreadLines int      // lines of those that did not decode
	Silent      int      // transcripts that hold no conversation row
	Unreadable  []string // session ids
	Facts       []Fact   // in the order first seen
	// Drift says why the reader's rules may no longer fit the format, one
	// reason each; it is empty when they still do.
	Drift []string
}

// Fact is one signal summed over a survey.
type Fact struct {
	Signal
	MissedIn []string // the short ids of the sessions with a miss
}

// Survey reads every transcript changed in the window before now. A store
// that cannot be listed is an error: a survey of what it never read would
// report no drift.
func (l *Loader) Survey(now time.Time) (Survey, error) {
	ids, err := l.Recent(now.Add(-SurveyWindow))
	if err != nil {
		return Survey{}, fmt.Errorf("the transcripts could not all be listed, so nothing was checked: %w", err)
	}
	records := make([]Record, len(ids))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Go(func() {
			gate <- struct{}{}
			rec := l.Load(id)
			rec.Events, rec.Notes = nil, nil
			records[i] = rec
			<-gate
		})
	}
	wg.Wait()

	s := Survey{Window: SurveyWindow, Transcripts: len(ids)}
	at := map[string]int{} // a fact's place in s.Facts
	for _, rec := range records {
		switch rec.Status {
		case Partial:
			s.Partial++
			s.UnreadLines += rec.UnreadLines
		case Unreadable:
			s.Unreadable = append(s.Unreadable, rec.ID)
			continue
		}
		if rec.Turns == 0 {
			s.Silent++
		}
		for _, sig := range rec.Signals {
			i, seen := at[sig.Fact]
			if !seen {
				i = len(s.Facts)
				at[sig.Fact] = i
				s.Facts = append(s.Facts, Fact{Fact: sig.Fact, Note: sig.Note})
			}
			f := &s.Facts[i]
			f.Primary += sig.Primary
			f.Second += sig.Second
			f.Missed += sig.Missed
			if sig.Missed > 0 {
				f.MissedIn = append(f.MissedIn, rec.ID[:8])
			}
		}
	}

	if len(s.Unreadable) > 0 {
		s.Drift = append(s.Drift, "a transcript is unreadable")
	}
	// Claude Code writes whole lines, so a line it cannot have written in the
	// shape the reader knows means the shape moved.
	if s.Partial > 0 {
		s.Drift = append(s.Drift, "lines do not decode")
	}
	// Every transcript without a conversation row means the row types moved.
	if len(ids) > 0 && s.Silent+len(s.Unreadable) == len(ids) {
		s.Drift = append(s.Drift, "no transcript holds a conversation row")
	}
	for _, f := range s.Facts {
		// A second trace is not a perfect superset: Claude Code itself now
		// and then leaves a created pull request without its link row. One
		// miss in ten is past what that explains.
		if f.Missed*MissTolerance > f.Second {
			s.Drift = append(s.Drift, fmt.Sprintf("the reader missed %d of %d for %q", f.Missed, f.Second, f.Fact))
		}
	}
	return s, nil
}
