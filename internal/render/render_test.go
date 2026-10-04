package render

import (
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
)

// A time is said two ways, in a sentence and in a cell, and both name the
// same unit at every boundary.
func TestOneTimeInTwoForms(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	v := View{Now: now}
	for _, c := range []struct {
		back        time.Duration
		long, short string
	}{
		{0, "now", "now"},
		{999 * time.Millisecond, "now", "now"},
		{time.Second, "1 second ago", "1s"},
		{59 * time.Second, "59 seconds ago", "59s"},
		{time.Minute, "1 minute ago", "1m"},
		{59*time.Minute + 59*time.Second, "59 minutes ago", "59m"},
		{time.Hour, "1 hour ago", "1h"},
		{24*time.Hour - time.Second, "23 hours ago", "23h"},
		{24 * time.Hour, "1 day ago", "1d"},
		{7*day - time.Second, "6 days ago", "6d"},
		{7 * day, "1 week ago", "1w"},
		{29 * day, "4 weeks ago", "4w"},
		{30 * day, "1 month ago", "1mo"},
		{364 * day, "12 months ago", "12mo"},
		{365 * day, "1 year ago", "1y"},
		{800 * day, "2 years ago", "2y"},
		{-5 * time.Minute, "5 minutes from now", "in 5m"},
	} {
		at := now.Add(-c.back)
		if long, short := v.ago(at), v.brief(at); long != c.long || short != c.short {
			t.Errorf("%v back: %q and %q, want %q and %q", c.back, long, short, c.long, c.short)
		}
	}
	if long, short := v.ago(time.Time{}), v.brief(time.Time{}); long != "at an unknown time" || short != "?" {
		t.Errorf("no time: %q and %q", long, short)
	}
}

// Text is cut by the columns it takes on screen, where a CJK character takes
// two.
func TestCutCountsColumns(t *testing.T) {
	for _, c := range []struct {
		text string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"abcd", 3, "ab…"},
		{"a word and more", 8, "a word…"}, // no space before the mark
		{"日历只走一遍", 12, "日历只走一遍"},
		{"日历只走一遍", 8, "日历只…"},
		{"日历只走一遍", 7, "日历只…"},
		{"abc", 0, ""},
	} {
		if got := cut(c.text, c.n); got != c.want || width(got) > max(c.n, 0) {
			t.Errorf("cut(%q, %d) = %q (%d columns), want %q", c.text, c.n, got, width(got), c.want)
		}
	}
	if got := clip("a  note\nover two lines", 40); got != "a note over two lines" {
		t.Errorf("clip: %q", got)
	}
}

// A cell of two styles is cut as its text would be, and the mark takes the
// style of the span the cut falls in: a warning cut short stays a warning,
// and one that fits whole leaves the mark to the note after it.
func TestACellIsCutBySpans(t *testing.T) {
	painted := View{Color: true}
	for _, c := range []struct {
		warning, note string
		n             int
		want          string
	}{
		{"[1 line could not be read]", " a note that runs on", 46, "\x1b[31m[1 line could not be read]\x1b[0m a note that runs on"},
		{"[1 line could not be read]", " a note that runs on", 30, "\x1b[31m[1 line could not be read]\x1b[0m a…"}, // no space before the mark
		{"[1 line could not be read]", " a note that runs on", 27, "\x1b[31m[1 line could not be read]\x1b[0m…"},
		{"[1 line could not be read]", " a note that runs on", 26, "\x1b[31m[1 line could not be read…\x1b[0m"},
		{"[1 line could not be read]", " a note that runs on", 10, "\x1b[31m[1 line c…\x1b[0m"},
		{"[日历]", " 只走一遍", 10, "\x1b[31m[日历]\x1b[0m 只…"},
		{"[日历]", " 只走一遍", 9, "\x1b[31m[日历]\x1b[0m…"},
		{"[日历]", " 只走一遍", 4, "\x1b[31m[日…\x1b[0m"},
		{"[日历]", " 只走一遍", 0, ""},
	} {
		if got := painted.paint(of(problem, c.warning).add(plain, c.note).cut(c.n)); got != c.want {
			t.Errorf("%q and %q cut to %d: %q, want %q", c.warning, c.note, c.n, got, c.want)
		}
	}
	// At every width the text is what cutting the whole string gives.
	for _, parts := range [][2]string{{"[1 line could not be read]", " (2) a note"}, {"[日历]", " 只走一遍"}, {"!", " a title"}} {
		whole := parts[0] + parts[1]
		for n := range width(whole) + 2 {
			if got := (View{}).paint(of(problem, parts[0]).add(plain, parts[1]).cut(n)); got != cut(whole, n) {
				t.Errorf("%q cut to %d: %q, the string gives %q", whole, n, got, cut(whole, n))
			}
		}
	}
}

// Every colour a label may name has a hue, and a label that names none takes
// one by its place.
func TestEveryLabelColourHasAHue(t *testing.T) {
	for _, name := range append(config.Colors, cycle...) {
		if hues[name] == plain {
			t.Errorf("no hue for %q", name)
		}
	}
	v := View{Labels: []config.Label{{Name: "a"}, {Name: "b", Color: "yellow"}, {Name: "c"}, {Name: "d"}, {Name: "e"}}}
	for name, want := range map[string]style{"a": hues["blue"], "b": hues["yellow"], "c": hues["cyan"], "d": hues["green"], "e": hues["blue"], "none": plain} {
		if got := v.hue(name); got != want {
			t.Errorf("hue(%q) = %q, want %q", name, got, want)
		}
	}
}

// A wide cell runs over the columns after it without widening its own, and a
// column no row fills takes no room.
func TestLay(t *testing.T) {
	v := View{}
	rows := [][]cell{
		{of(plain, "a"), {}, of(plain, "bb"), of(plain, "c")},
		{of(plain, "aaa"), {}, {spans: []span{{"a long reason", plain}}, wide: true}, of(plain, "d")},
		{{}, {}, {}, of(plain, "e")},
	}
	want := []string{"a    bb  c", "aaa  a long reason  d", "         e"}
	for i, got := range v.lay(rows, 2) {
		if got != want[i] {
			t.Errorf("row %d: %q, want %q", i, got, want[i])
		}
	}
}
