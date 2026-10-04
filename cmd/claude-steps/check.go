package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/qiushiyan/claude-steps/internal/record"
)

const (
	// checkWindow is how far back `check` reads.
	checkWindow = 7 * 24 * time.Hour
	// missTolerance: `check` fails a fact when more than one in this many of
	// its second traces has no match in the reader's own count.
	missTolerance = 10
)

// check reads every transcript changed in the last week through the same
// loader the views use, and reports each fact counted two ways.
func (a *app) check(args []string) error {
	_, rest, err := flags(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return errors.New("check takes no arguments")
	}
	ids, err := a.loader.Recent(a.now().Add(-checkWindow))
	if err != nil {
		return fmt.Errorf("the transcripts could not all be listed, so nothing was checked: %w", err)
	}
	records := make([]record.Record, len(ids))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Go(func() {
			gate <- struct{}{}
			rec := a.loader.Load(id)
			rec.Events, rec.Notes = nil, nil
			records[i] = rec
			<-gate
		})
	}
	wg.Wait()

	var partial, unreadLines, silent int
	var unreadable []string
	var facts []string
	totals := map[string]*record.Signal{}
	missedIn := map[string][]string{}
	for _, rec := range records {
		switch rec.Status {
		case record.Partial:
			partial++
			unreadLines += rec.UnreadLines
		case record.Unreadable:
			unreadable = append(unreadable, rec.ID)
			continue
		}
		if rec.Turns == 0 {
			silent++
		}
		for _, sig := range rec.Signals {
			t := totals[sig.Fact]
			if t == nil {
				t = &record.Signal{Fact: sig.Fact, Note: sig.Note}
				totals[sig.Fact] = t
				facts = append(facts, sig.Fact)
			}
			t.Primary += sig.Primary
			t.Second += sig.Second
			t.Missed += sig.Missed
			if sig.Missed > 0 {
				missedIn[sig.Fact] = append(missedIn[sig.Fact], rec.ID[:8])
			}
		}
	}

	w := a.stdout
	fmt.Fprintf(w, "%d transcripts changed in the last 7 days\n", len(ids))
	if partial > 0 {
		fmt.Fprintf(w, "%d read in part, %d lines could not be decoded\n", partial, unreadLines)
	}
	if silent > 0 {
		fmt.Fprintf(w, "%d hold no conversation row\n", silent)
	}
	for _, id := range unreadable {
		fmt.Fprintf(w, "unreadable: %s\n", id)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-18s %8s %8s %8s   %s\n", "fact", "reader", "second", "missed", "reader's rule / second trace")
	var drift []string
	if len(unreadable) > 0 {
		drift = append(drift, "a transcript is unreadable")
	}
	// Claude Code writes whole lines, so a line it cannot have written in the
	// shape the reader knows means the shape moved.
	if partial > 0 {
		drift = append(drift, "lines do not decode")
	}
	// Every transcript without a conversation row means the row types moved.
	if len(ids) > 0 && silent+len(unreadable) == len(ids) {
		drift = append(drift, "no transcript holds a conversation row")
	}
	for _, fact := range facts {
		t := totals[fact]
		fmt.Fprintf(w, "%-18s %8d %8d %8d   %s\n", fact, t.Primary, t.Second, t.Missed, t.Note)
		if t.Missed > 0 {
			fmt.Fprintf(w, "%-18s missed in %s\n", "", strings.Join(missedIn[fact], " "))
		}
		// A second trace is not a perfect superset: Claude Code itself now
		// and then leaves a created pull request without its link row. One
		// miss in ten is past what that explains.
		if t.Missed*missTolerance > t.Second {
			drift = append(drift, fmt.Sprintf("the reader missed %d of %d for %q", t.Missed, t.Second, fact))
		}
	}
	fmt.Fprintln(w)
	if len(drift) > 0 {
		fmt.Fprintln(w, "the transcript format may have changed: "+strings.Join(drift, "; "))
		return errors.New("check found drift")
	}
	fmt.Fprintln(w, "no drift: the second traces saw nothing the reader's rules keep missing")
	return nil
}
