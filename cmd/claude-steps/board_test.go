package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/fixture"
	"github.com/qiushiyan/claude-steps/internal/panes"
)

// A label's cell is a date, never a judgement. This is the whole grammar,
// and the grammar of the cell that dates the newest round with no collect.
const when = `(now|\d+(s|m|h|d|w|mo|y))`

var (
	cell        = regexp.MustCompile(`^(·|((read|pasted) )?` + when + `( \+\d+)?|named ` + when + `)$`)
	uncollected = regexp.MustCompile(`^(·|` + when + `( ×\d+)?)$`)
	columns     = regexp.MustCompile(` {2,}`)
)

// Obligations 5, 10 and 12 on the board.
func TestBoard(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%3", "transcript deleted, the PR was merged")
	out := w.ok("board")
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 6 {
		t.Fatalf("want a header and five panes (the pane with a malformed id is not a session):\n%s", out)
	}
	if got := columns.Split(rows[0], -1); !slices.Equal(got, []string{"pane", "session", "review", "verify", "prompts", "no collect", "PR", "note"}) {
		t.Errorf("header: %q", got)
	}
	contains(t, rows[1], "work:1.1  The calendar walks days once  2h +1   15m +0    read 12m  ·           #7145")
	contains(t, rows[2], "work:1.2", "named 3h")
	contains(t, rows[3], "work:2.1", "no transcript", "transcript deleted, the PR was merged")
	contains(t, rows[4], "work:2.2", "transcript unreadable")
	// A transcript read with a line missing is marked before its title, and
	// says so in words where there is room.
	contains(t, rows[5], "work:3.1  ! app", "[1 line could not be read]")

	// Every label cell of every row fits the grammar.
	idRows := strings.Split(strings.TrimRight(w.ok("board", "--ids"), "\n"), "\n")
	header := columns.Split(strings.Split(idRows[0], "\t")[2], -1)
	for _, i := range []int{1, 2, 5} {
		fields := strings.Split(idRows[i], "\t")
		cells := columns.Split(fields[2], -1)
		for col := 2; col < 5; col++ {
			if !cell.MatchString(cells[col]) {
				t.Errorf("row %d, label %s: cell %q is not a date", i, header[col], cells[col])
			}
		}
		if !uncollected.MatchString(cells[5]) {
			t.Errorf("row %d: %q is not a date", i, cells[5])
		}
	}
	// --ids: the pane id and the session id lead every row, and are empty on
	// the header.
	if !strings.HasPrefix(idRows[0], "\t\tpane") || !strings.HasPrefix(idRows[1], "%1\t"+worked+"\twork:1.1") {
		t.Errorf("--ids rows:\n%s\n%s", idRows[0], idRows[1])
	}
}

// reviewVerify is the text of a project's snippet after the "/review " it
// opens with, and promptCheck a second one that opens with no command.
const (
	reviewVerify = "full review. While you wait, run pl-loopy-verify\non the local rig and compare against a baseline."
	promptCheck  = "Review and revise the prompts this session's work touched against the project's guide."
)

// projectSnippets names a project's own snippet file in the world's
// configuration, beside the global one, and lists its keys: the first under
// review and verify, the second under prompts, which counts no commits. It
// returns the file.
func projectSnippets(t *testing.T, w *world) string {
	t.Helper()
	local := filepath.Join(w.home, "work", "app", ".tabtype.local.toml")
	labels := strings.Replace(configFile, `skills = ["pl-loopy-verify"]`, `skills = ["pl-loopy-verify"]`+"\n"+`snippets = ["app-review-verify"]`, 1)
	labels = strings.Replace(labels, `["review-implementation"]`, `["review-implementation", "app-review-verify"]`, 1)
	labels = strings.Replace(labels, `skills = ["prompt-engineering"]`, `skills = ["prompt-engineering"]`+"\n"+`snippets = ["app-prompt-check"]`, 1)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"),
		[]byte(`snippets = ["~/.config/tabtype/config.toml", "~/work/app/.tabtype.local.toml"]`+"\n"+labels))
	fixture.WriteFile(t, local, []byte(`
[[snippets]]
key = "app-review-verify"
expand = """
/review full review. While you wait, run pl-loopy-verify
on the local rig and compare against a baseline.
"""

[[snippets]]
key = "app-prompt-check"
expand = "`+promptCheck+`"
`))
	return local
}

// A project keeps snippets of its own in a file the configuration names. A
// paste from it is the dated event of each label that lists its key, with the
// commits made since where the label counts them, and the cell says it is a
// paste: a request never stands as a run. Without the file the same
// transcript reads as it did before, as a prompt that names a skill.
func TestAPasteFromAProjectsFile(t *testing.T) {
	w := newWorld(t)
	local := projectSnippets(t, w)
	const pasting = "ffffffff-6666-4666-8666-666666666666"
	tr := fixture.New().Title("Rows keep their order")
	tr.Prompt("<pasted_content id=\"4463\">\n/review " + reviewVerify + "\n</pasted_content>")
	tr.Bash(`git commit -q -m "rows keep their order"`, "")
	tr.At(fixture.Start.Add(time.Hour))
	tr.Slash("review", reviewVerify, "/home/u/.claude/skills/review")
	tr.Prompt(promptCheck)
	tr.Bash(`git commit -q -m "after the second request"`, "")
	tr.Write(t, w.projects, "-work-app", pasting)
	w.panes = []panes.Pane{{ID: "%7", Where: "work:4.1", SessionID: pasting}}

	// review holds the command's run and verify the paste that came with it;
	// prompts counts no commits, and its paste is said all the same.
	row := strings.Split(strings.TrimRight(w.ok("board"), "\n"), "\n")[1]
	cells := columns.Split(row, -1)
	if got := cells[2:5]; !slices.Equal(got, []string{"2h +1", "pasted 2h +1", "pasted 2h"}) {
		t.Errorf("review, verify and prompts: %q in %q", got, row)
	}
	for _, c := range cells[2:5] {
		if !cell.MatchString(c) {
			t.Errorf("cell %q is not a date", c)
		}
	}
	contains(t, w.ok("show", "%7"),
		"review    2 hours ago   1 commit since   /review full review.",
		"verify    2 hours ago   1 commit since   pasted app-review-verify",
		"  verify          2h   pasted app-review-verify\n",
		"  review          2h   /review  full review.",
		"  review verify   3h   pasted app-review-verify\n",
	)
	// The paste names the skill its own command ran.
	contains(t, w.ok("show", "%7", "--json"), `"name": "app-review-verify",`+"\n"+`      "command": "review"`)

	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	contains(t, w.ok("board"), "2h +1   named 3h")
	out := w.ok("show", "%7")
	contains(t, out, `you: "/review full review. While you wait, run pl-loopy-verify on…"`)
	lacks(t, out, "pasted")
}

// The label cells never give way, and a cell that says what its event is
// takes more room than a date. When the cells leave the title less than its
// floor, the columns close up to one space before a row runs past the width.
func TestTheBoardClosesUpBeforeItRunsOver(t *testing.T) {
	w := newWorld(t)
	var labels strings.Builder
	for _, name := range []string{"consult", "spec", "review", "verify", "docs", "pr-review", "prompts"} {
		fmt.Fprintf(&labels, "[[label]]\nname = %q\nsnippets = [%q]\ncount_commits = %t\n\n", name, "ask-"+name, name == "verify")
	}
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), []byte(labels.String()))
	const ask = " the session to go over the work once more and report what it finds."
	var snippets strings.Builder
	tr := fixture.New().Title("Rows keep their order")
	for _, name := range []string{"spec", "verify", "prompts"} {
		fmt.Fprintf(&snippets, "[[snippets]]\nkey = %q\nexpand = %q\n\n", "ask-"+name, "For "+name+": ask"+ask)
		tr.Prompt("For " + name + ": ask" + ask)
	}
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "tabtype", "config.toml"), []byte(snippets.String()))
	tr.Bash(`git commit -q -m "rows keep their order"`, "")
	id := fixture.ID("abab7878")
	tr.Write(t, w.projects, "p", id)
	w.panes = []panes.Pane{{ID: "%7", Where: "work:4.1", SessionID: id}}

	board := func(cols int) []string {
		t.Helper()
		w.env = map[string]string{"COLUMNS": fmt.Sprint(cols)}
		out := strings.Split(strings.TrimRight(w.ok("board"), "\n"), "\n")
		for _, row := range out {
			if screen.StringWidth(row) > cols {
				t.Errorf("at %d columns a row takes %d:\n%s", cols, screen.StringWidth(row), row)
			}
		}
		return out
	}
	// Room for the cells and a title: the columns stand two spaces apart.
	out := board(120)
	contains(t, out[0], "consult  spec       review  verify        docs  pr-review  prompts    no collect  PR")
	contains(t, out[1], "·        pasted 3h  ·       pasted 3h +1  ·     ·          pasted 3h  ·           ·")
	// No room at two spaces: one, with every cell whole and under its header.
	out = board(100)
	contains(t, out[0], "consult spec      review verify       docs pr-review prompts   no collect PR")
	// The title takes back what closing up leaves over.
	contains(t, out[1], "Rows keep the… ·       pasted 3h ·      pasted 3h +1 ·    ·         pasted 3h ·          ·")
}

// Colour is the same text painted: with the escapes removed it is the plain
// output byte for byte. The popup reads through a pipe, so it asks for colour
// in the environment, and the ids it acts on stay bare.
func TestColourIsTheSameTextPainted(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "a note")
	force := map[string]string{"CLICOLOR_FORCE": "1"}
	for _, args := range [][]string{{"board"}, {"board", "--ids"}, {"show", "%1"}, {"show", "%1", "--all"}, {"show", "%3"}, {"show", "%4"}, {"show", "%5"}} {
		w.env = nil
		bare := w.ok(args...)
		w.env = force
		painted := w.ok(args...)
		if escape.MatchString(bare) || !escape.MatchString(painted) {
			t.Errorf("%v: colour without being asked, or none when asked:\n%q", args, painted)
		}
		if got := escape.ReplaceAllString(painted, ""); got != bare {
			t.Errorf("%v: the painted text differs from the plain:\n%s\n%s", args, got, bare)
		}
	}
	for _, row := range strings.Split(strings.TrimRight(w.ok("board", "--ids"), "\n"), "\n") {
		if fields := strings.SplitN(row, "\t", 3); len(fields) != 3 || strings.Contains(fields[0]+fields[1], "\x1b") {
			t.Errorf("an id field is not bare: %q", row)
		}
	}

	// A hue names a label, red marks what could not be read, and a count of
	// commits is strong only when there are any.
	contains(t, w.ok("board"),
		"\x1b[34mreview\x1b[0m", "\x1b[35mverify\x1b[0m", "\x1b[36mprompts\x1b[0m",
		"2h \x1b[1m+1\x1b[0m", "15m +0", "\x1b[2m·\x1b[0m",
		"\x1b[31mno transcript\x1b[0m", "\x1b[31m!\x1b[0m app", "\x1b[31m[1 line could not be read]\x1b[0m",
	)
	contains(t, w.ok("show", "%1"),
		"\x1b[34mreview\x1b[0m    2 hours ago      \x1b[1m1 commit since\x1b[0m",
		"\x1b[35mverify\x1b[0m    15 minutes ago   0 commits since",
		"  \x1b[34mreview\x1b[0m    3h    review-r1  /review  codex full review",
		"  \x1b[2mnote\x1b[0m      now   a note",
	)
	contains(t, w.ok("show", "%5"), "\x1b[31m1 line could not be read\x1b[0m")

	// NO_COLOR wins over being asked, and a terminal is painted unasked.
	w.env = map[string]string{"CLICOLOR_FORCE": "1", "NO_COLOR": "1"}
	if escape.MatchString(w.ok("board")) {
		t.Error("NO_COLOR did not turn colour off")
	}
	w.env, w.tty = map[string]string{"CLICOLOR_FORCE": "0"}, false
	if escape.MatchString(w.ok("board")) {
		t.Error("CLICOLOR_FORCE=0 asked for colour")
	}
	w.env, w.tty = nil, true
	if !escape.MatchString(w.ok("board")) {
		t.Error("a terminal got no colour")
	}
}

// When the popup says how wide it is, the title and the note give way and
// the label cells never do. A note with no room is left to the session view,
// and the mark of a transcript read with something missing stays.
func TestTheBoardFitsTheWidth(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "a long note that runs well past the edge of a narrow popup window")
	wide := fixture.ID("abcd9876")
	fixture.New().Title("日历只走一遍，其余的都是多余").Slash("review", "", "/home/u/.claude/skills/review").Write(t, w.projects, "p", wide)
	w.panes = append(w.panes, panes.Pane{ID: "%7", Where: "work:4.1", SessionID: wide})

	rows := func(cols int) []string {
		t.Helper()
		w.env = map[string]string{"COLUMNS": fmt.Sprint(cols)}
		out := strings.Split(strings.TrimRight(w.ok("board"), "\n"), "\n")
		for _, row := range out {
			if screen.StringWidth(row) > cols {
				t.Errorf("at %d columns a row takes %d:\n%s", cols, screen.StringWidth(row), row)
			}
		}
		contains(t, out[1], "2h +1   15m +0    read 12m  ·           #7145")
		contains(t, out[2], "named 3h")
		contains(t, out[5], "! app")
		// A title in CJK takes two columns a character, and the columns
		// after it still line up.
		if at, want := columnOf(t, out[6], "3h +0"), columnOf(t, out[0], "review"); at != want {
			t.Errorf("at %d columns the label cell of a CJK title starts at %d, the header at %d:\n%s\n%s", cols, at, want, out[0], out[6])
		}
		return out
	}
	// Room for everything: the note is cut only at its own limit.
	out := rows(140)
	contains(t, out[0], "note")
	contains(t, out[1], "The calendar walks days once  ", "a long note that runs well past the edge of a n…")
	// The title gives way first, then the note.
	out = rows(110)
	contains(t, out[1], "The calendar walks…  ", "a long note that runs well pas…")
	out = rows(90)
	contains(t, out[1], "The calendar w…  ", "a long note tha…")
	// No room for a note: the column goes, and the mark stays.
	out = rows(70)
	lacks(t, out[0], "note")
	lacks(t, out[1], "a long note")
	contains(t, out[1], "The calenda…  ")
}
