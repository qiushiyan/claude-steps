package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/record"
)

// Survey prints what `check` found: what was read, each fact counted two
// ways, and the drift, if any.
func Survey(w io.Writer, s record.Survey) {
	fmt.Fprintf(w, "%d transcripts changed in the last %d days\n", s.Transcripts, int(s.Window/(24*time.Hour)))
	if s.Partial > 0 {
		fmt.Fprintf(w, "%d read in part, %d lines could not be decoded\n", s.Partial, s.UnreadLines)
	}
	if s.Silent > 0 {
		fmt.Fprintf(w, "%d hold no conversation row\n", s.Silent)
	}
	for _, id := range s.Unreadable {
		fmt.Fprintf(w, "unreadable: %s\n", id)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-18s %8s %8s %8s   %s\n", "fact", "reader", "second", "missed", "reader's rule / second trace")
	for _, f := range s.Facts {
		fmt.Fprintf(w, "%-18s %8d %8d %8d   %s\n", f.Fact, f.Primary, f.Second, f.Missed, f.Note)
		if len(f.MissedIn) > 0 {
			fmt.Fprintf(w, "%-18s missed in %s\n", "", strings.Join(f.MissedIn, " "))
		}
	}
	fmt.Fprintln(w)
	if len(s.Drift) > 0 {
		fmt.Fprintln(w, "the transcript format may have changed: "+strings.Join(s.Drift, "; "))
		return
	}
	fmt.Fprintln(w, "no drift: the second traces saw nothing the reader's rules keep missing")
}
