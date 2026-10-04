package render

import (
	"fmt"
	"time"
)

// unit is one size of time span, named as a sentence names it and as a cell
// does.
type unit struct {
	long, short string
	d           time.Duration
}

const day = 24 * time.Hour

// Largest first. A month is thirty days and a year 365: the span is for
// reading, not for arithmetic.
var units = []unit{
	{"year", "y", 365 * day},
	{"month", "mo", 30 * day},
	{"week", "w", 7 * day},
	{"day", "d", day},
	{"hour", "h", time.Hour},
	{"minute", "m", time.Minute},
	{"second", "s", time.Second},
}

// since is how far at lies from now, in the largest unit that fits whole;
// n is 0 inside a second. Both forms of a time come from it, so a cell and a
// sentence cannot name different units for one event.
func (v View) since(at time.Time) (n int, u unit, future bool) {
	d := v.Now.Sub(at)
	if future = d < 0; future {
		d = -d
	}
	for _, u := range units {
		if d >= u.d {
			return int(d / u.d), u, future
		}
	}
	return 0, unit{}, false
}

// ago says when something happened in words: "11 minutes ago".
func (v View) ago(at time.Time) string {
	if at.IsZero() {
		return "at an unknown time"
	}
	n, u, future := v.since(at)
	switch {
	case n == 0:
		return "now"
	case future:
		return plural(n, u.long) + " from now"
	}
	return plural(n, u.long) + " ago"
}

// brief is the same time as a cell holds it: "11m".
func (v View) brief(at time.Time) string {
	if at.IsZero() {
		return "?"
	}
	n, u, future := v.since(at)
	switch {
	case n == 0:
		return "now"
	case future:
		return fmt.Sprintf("in %d%s", n, u.short)
	}
	return fmt.Sprintf("%d%s", n, u.short)
}
