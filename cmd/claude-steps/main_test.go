package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/qiushiyan/claude-steps/internal/fixture"
	"github.com/qiushiyan/claude-steps/internal/panes"
)

const (
	worked  = "aaaaaaaa-1111-4111-8111-111111111111" // a session with a full timeline
	quiet   = "bbbbbbbb-2222-4222-8222-222222222222" // only a prompt that names a skill
	gone    = "cccccccc-3333-4333-8333-333333333333" // a live pane whose transcript is not on disk
	garbled = "dddddddd-4444-4444-8444-444444444444" // a file no row of which decodes
	torn    = "eeeeeeee-5555-4555-8555-555555555555" // readable, with one bad line
)

const configFile = `
[[label]]
name = "review"
skills = ["review"]
snippets = ["review-implementation"]
jobs = ["review-"]
count_commits = true

[[label]]
name = "verify"
skills = ["pl-loopy-verify"]
count_commits = true

[[label]]
name = "prompts"
skills = ["prompt-engineering"]
`

const snippetFile = `
[[snippets]]
key = "review-implementation"
expand = '''
Review the implementation against the spec, obligation by obligation, and report every gap.
'''
`

const collected = "job: /home/u/.local/state/envoy/jobs/app-1/%s\nstatus: %s\nduration: 6m\n\n--- result.md ---\nfindings"

// world is a temporary home with transcripts, configuration and a fixed set
// of tmux panes. tmux itself is substituted at the one function that calls
// it, so these tests cannot prove the list-panes format string.
type world struct {
	t        *testing.T
	home     string
	projects string
	state    string
	now      time.Time
	panes    []panes.Pane
	panesErr error
	tmuxPane string
	env      map[string]string // COLUMNS, NO_COLOR, CLICOLOR_FORCE
	tty      bool              // stdout is a terminal
}

func newWorld(t *testing.T) *world {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	w := &world{t: t, home: home, projects: filepath.Join(home, ".claude", "projects"),
		state: filepath.Join(home, ".local", "state", "claude-steps"), now: fixture.Start.Add(3*time.Hour + 30*time.Minute)}
	fixture.WriteFile(t, filepath.Join(home, ".config", "claude-steps", "config.toml"), []byte(configFile))
	fixture.WriteFile(t, filepath.Join(home, ".config", "tabtype", "config.toml"), []byte(snippetFile))

	// The first block of rows starts three and a half hours before "now",
	// the second two and a half, so each reads as a whole number of hours.
	tr := fixture.New().Title("The calendar walks days once")
	tr.Slash("review", "codex full review", "/home/u/.claude/skills/review")
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash(`git commit -q -m "the calendar walks days once (review r1)"`, "")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "ok"))
	tr.At(fixture.Start.Add(time.Hour))
	tr.Bash("envoy run review-r2 --with @review-r1 --prompt-file /tmp/r2.md", "Command running in background")
	tr.Bash("envoy collect review-r2", fmt.Sprintf(collected, "review-r2", "partial"))
	tr.Compaction("manual", false, "summary of the session")
	tr.Bash(`git commit -q -m "docs: the stories on the local rig"`, "")
	tr.Bash("gh pr create --fill", "https://github.com/acme/app/pull/7145")
	tr.PRLink("acme/app", 7145)
	tr.At(w.now.Add(-15 * time.Minute))
	tr.SkillCall("pl-loopy-verify", "local spikes", "p", false)
	tr.Read("/home/u/.claude/skills/prompt-engineering/SKILL.md", false)
	tr.Write(t, w.projects, "-work-app", worked)

	fixture.New().Prompt("have we run pl-loopy-verify yet? I think it is done").Write(t, w.projects, "-work-other", quiet)
	fixture.WriteFile(t, filepath.Join(w.projects, "-work-app", garbled+".jsonl"), []byte("<<<<<<< not a transcript\nnor this\n"))
	bad := append(fixture.New().Slash("review", "", "/home/u/.claude/skills/review").Bytes(), []byte("{\"type\":\"user\",\n")...)
	fixture.WriteFile(t, filepath.Join(w.projects, "-work-app", torn+".jsonl"), append(bad, fixture.New().Prompt("more").Bytes()...))

	w.panes = []panes.Pane{
		{ID: "%1", Where: "work:1.1", SessionID: worked},
		{ID: "%2", Where: "work:1.2", SessionID: quiet},
		{ID: "%3", Where: "work:2.1", SessionID: gone},
		{ID: "%4", Where: "work:2.2", SessionID: garbled},
		{ID: "%5", Where: "work:3.1", SessionID: torn},
		{ID: "%6", Where: "work:3.2", SessionID: "not-a-session-id"},
	}
	return w
}

func (w *world) run(args ...string) (stdout, stderr string, code int) {
	w.t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdout: &out, stderr: &errb, tty: w.tty, now: func() time.Time { return w.now },
		panes: func() ([]panes.Pane, error) { return w.panes, w.panesErr },
		getenv: func(k string) string {
			if k == "TMUX_PANE" {
				return w.tmuxPane
			}
			return w.env[k]
		}}
	code = a.run(args)
	return out.String(), errb.String(), code
}

func (w *world) ok(args ...string) string {
	w.t.Helper()
	out, errb, code := w.run(args...)
	if code != 0 {
		w.t.Fatalf("claude-steps %s: exit %d: %s", strings.Join(args, " "), code, errb)
	}
	return out
}

func contains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func lacks(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(text, u) {
			t.Errorf("unexpected %q in:\n%s", u, text)
		}
	}
}

// Obligations 3, 4 and 17 as the user reads them.
func TestShow(t *testing.T) {
	w := newWorld(t)
	out := w.ok("show", "%1")
	// What the session is, on two lines and under a rule.
	contains(t, out,
		"The calendar walks days once   aaaaaaaa   work:1.1\n"+
			"/work/app  feat/thing   1 compaction, last 2 hours ago   PR #7145 opened here  https://github.com/acme/app/pull/7145\n"+
			strings.Repeat("─", 72)+"\n",
	)
	// The label lines come first: the latest review event is the second
	// dispatch, and one commit was made since.
	contains(t, out,
		"review    2 hours ago      1 commit since    review-r2 collected 2 hours ago, envoy said partial\n"+
			"verify    15 minutes ago   0 commits since   skill pl-loopy-verify local spikes\n"+
			"prompts   12 minutes ago                     read skills/prompt-engineering/SKILL.md\n",
		"no collect seen   ·\nnotes             none\n",
	)
	// The steps, newest first: what is under a label, a round as one line at
	// its dispatch with the skill run before it, and the commits between as a
	// count.
	contains(t, out, `
steps
  prompts   12m   read skills/prompt-engineering/SKILL.md
  verify    15m   skill pl-loopy-verify  local spikes
                  1 commit
  review    2h    review-r2  envoy said partial
                  1 commit
  review    3h    review-r1  /review  codex full review

11 rows in the full history (show --all)
`)
	lacks(t, out, "compaction (manual)", "commit  ", "history\n", "dispatched", "  collected")

	// --all prints the whole timeline in the steps' place, and the footer's
	// count is the rows it holds.
	all := w.ok("show", "%1", "--all")
	_, history, found := strings.Cut(all, "\nhistory\n")
	if rows := strings.Count(history, "\n"); !found || rows != 11 {
		t.Errorf("want 11 rows of history, got %d:\n%s", rows, all)
	}
	contains(t, all,
		"  prompts   12m   read skills/prompt-engineering/SKILL.md\n",
		"  review    2h    review-r2  collected, envoy said partial\n  review    2h    review-r2  dispatched\n",
		"            2h    PR #7145 opened here  acme/app\n",
		"            2h    commit  docs: the stories on the local rig\n",
		"            2h    compaction (manual)\n",
		"            3h    commit  the calendar walks days once (review r1)\n",
		"  review    3h    /review  codex full review\n",
	)
	lacks(t, all, "steps\n", "in the full history")
	if head, _, _ := strings.Cut(out, "\nsteps\n"); !strings.HasPrefix(all, head) {
		t.Errorf("--all changes more than the timeline:\n%s", all)
	}

	// With no argument, show reads the pane it runs in.
	w.tmuxPane = "%1"
	if again := w.ok("show"); again != out {
		t.Errorf("show with no argument differs from show %%1")
	}
	// The board's JSON holds every live session.
	var board []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(w.ok("board", "--json")), &board); err != nil || len(board) != 5 || board[0].ID != worked {
		t.Errorf("board --json: %v %+v", err, board)
	}

	// A session id works with no tmux at all.
	w.panesErr = panes.ErrNoServer
	contains(t, w.ok("show", "aaaaaaaa"), "The calendar walks days once   aaaaaaaa\n")
}

// A round collected long after its dispatch is one step, dated at the
// dispatch as its label is: the code the reviewer read. The label's row and
// the full history say when the collect happened.
func TestARoundIsOneStepAtItsDispatch(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("ffffffff")
	tr := fixture.New()
	tr.Bash("envoy run review-r7 --with codex --prompt-file /tmp/r7.md", "Command running in background")
	tr.At(w.now.Add(-16 * time.Minute)) // the result row is a minute after the call
	tr.Bash("envoy collect review-r7", fmt.Sprintf(collected, "review-r7", "partial"))
	tr.Write(t, w.projects, "-work-app", id)
	contains(t, w.ok("show", id),
		"\nsteps\n  review   3h   review-r7  envoy said partial\n\n",
		"3 hours ago   0 commits since   review-r7 collected 15 minutes ago, envoy said partial\n",
	)
	contains(t, w.ok("show", id, "--all"),
		"  review   15m   review-r7  collected, envoy said partial\n"+
			"  review   3h    review-r7  dispatched\n",
	)
}

// Under a label that lists rounds a step is a round. The latest skill run
// before a round is on the round's line; a run no round took keeps a line of
// its own, and a round that followed another has nothing to carry.
func TestARoundCarriesTheSkillRunBeforeIt(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("abab5656")
	tr := fixture.New()
	tr.Slash("review", "goal", "/home/u/.claude/skills/review")
	tr.Slash("review", "codex full", "/home/u/.claude/skills/review")
	tr.Bash(`git commit -m "before the round"`, "")
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "ok"))
	tr.Bash(`git commit -m "fix (review r1)"`, "")
	tr.Bash("envoy run review-r2 --with codex --prompt-file /tmp/r2.md", "Command running in background")
	tr.SkillCall("pl-loopy-verify", "spikes", "p", false)
	tr.Bash("envoy collect /home/u/.local/state/envoy/jobs/app-1/review-r0", fmt.Sprintf(collected, "review-r0", "ok"))
	tr.Write(t, w.projects, "p", id)
	out := w.ok("show", id)
	contains(t, out, `
steps
  review   3h   review-r0  no dispatch seen
  verify   3h   skill pl-loopy-verify  spikes
  review   3h   review-r2  no collect seen
                1 commit
  review   3h   review-r1  /review  codex full
                1 commit
  review   3h   /review  goal

`)
	// The history keeps every line the steps folded.
	all := w.ok("show", id, "--all")
	contains(t, all, "review-r1  collected\n", "review-r1  dispatched\n", "/review  codex full\n", "/review  goal\n")
	lacks(t, all, "no dispatch seen")
}

// A name dispatched again before any collect is one round: envoy collects a
// name as its latest dispatch, so the earlier one is not waiting for a collect.
func TestANameDispatchedAgainIsOneRound(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("abab7878")
	tr := fixture.New()
	tr.Bash("envoy run review-r4 --with codex --prompt-file /tmp/r4.md", "Command running in background")
	tr.Bash("envoy run review-r4 --with codex --prompt-file /tmp/r4.md", "Command running in background")
	tr.Bash("envoy collect review-r4", fmt.Sprintf(collected, "review-r4", "ok"))
	tr.Bash("envoy run review-r5 --with codex --prompt-file /tmp/r5.md", "Command running in background")
	tr.Bash("envoy run review-r5 --with codex --prompt-file /tmp/r5.md", "Command running in background")
	tr.Write(t, w.projects, "p", id)
	w.panes = []panes.Pane{{ID: "%9", Where: "x:1.1", SessionID: id}}
	out := w.ok("show", id)
	contains(t, out,
		"no collect seen   review-r5   3 hours ago\nnotes",
		"\nsteps\n  review   3h   review-r5  dispatched 2 times, no collect seen\n  review   3h   review-r4  dispatched 2 times\n\n",
	)
	contains(t, w.ok("show", id, "--all"), "review-r4  dispatched  (2 times)\n")
	if cells := columns.Split(strings.Split(w.ok("board"), "\n")[1], -1); cells[5] != "3h" {
		t.Errorf("the board counts a name dispatched again once: %q", cells)
	}
}

// Obligation 17: --json carries timestamps and nothing relative.
func TestShowJSON(t *testing.T) {
	w := newWorld(t)
	out := w.ok("show", "%1", "--json")
	lacks(t, out, " ago", "from now")
	var got struct {
		Pane   string `json:"pane"`
		ID     string `json:"id"`
		Status string `json:"status"`
		Events []struct {
			At   time.Time `json:"at"`
			Kind string    `json:"kind"`
		} `json:"events"`
		PullRequests []struct {
			Number     int  `json:"number"`
			OpenedHere bool `json:"opened_here"`
		} `json:"pull_requests"`
		Compactions []json.RawMessage `json:"compactions"`
		Labels      []struct {
			Name         string `json:"name"`
			CommitsSince *int   `json:"commits_since"`
		} `json:"labels"`
		Notes []json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Pane != "%1" || got.ID != worked || got.Status != "ok" || len(got.Events) != 9 || !got.Events[0].At.Equal(fixture.Start) {
		t.Errorf("record: %+v", got)
	}
	if len(got.PullRequests) != 1 || !got.PullRequests[0].OpenedHere || len(got.Compactions) != 1 || got.Notes == nil {
		t.Errorf("pull requests, compactions, notes: %+v", got)
	}
	if len(got.Labels) != 3 || got.Labels[0].CommitsSince == nil || *got.Labels[0].CommitsSince != 1 {
		t.Errorf("labels: %+v", got.Labels)
	}
}

// A label's cell is a date, never a judgement. This is the whole grammar,
// and the grammar of the cell that dates the newest round with no collect.
const when = `(now|\d+(s|m|h|d|w|mo|y))`

var (
	cell        = regexp.MustCompile(`^(·|(read )?` + when + `( \+\d+)?|named ` + when + `)$`)
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

// Obligation 5: the tool's own words carry no verdict, and the user's words
// are shown as written.
func TestNoVerdictInTheToolsOwnWords(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{{"board"}, {"show", "%1"}, {"show", "%1", "--all"}, {"show", "%3"}, {"show", "%4"}, {"show", "%5"}} {
		out := strings.ToLower(w.ok(args...))
		lacks(t, out, "✓", "✔", "done", "passed", "stale", "fresh", "complete", "succeeded")
	}
	// His prompt says "done" and stays as he wrote it.
	contains(t, w.ok("show", "%2"), `you: "have we run pl-loopy-verify yet? I think it is done"`)
	w.ok("note", "%1", "review is done, docs passed")
	contains(t, w.ok("show", "%1"), "review is done, docs passed")
}

// Obligations 10 and 12 in the session view.
func TestUnreadTranscriptsSayWhy(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%3", "kept past the transcript")

	missing := w.ok("show", "%3")
	contains(t, missing, "session cccccccc   cccccccc   work:2.1\nno transcript\n", "notes   now   kept past the transcript")
	unreadable := w.ok("show", "%4")
	contains(t, unreadable, "transcript unreadable", filepath.Join("-work-app", garbled+".jsonl"), "notes   none")
	for _, out := range []string{missing, unreadable} {
		lacks(t, out, "no events in the transcript", " ago", "steps", "no collect seen")
	}

	// What the view may lack is said under the header, before the labels.
	partial := w.ok("show", "%5")
	contains(t, partial, "/work/app  feat/thing\n1 line could not be read\n─", "/review")

	// A transcript that was read and holds nothing says so in its own words.
	fresh := fixture.ID("f0f0f0f0")
	fixture.WriteFile(t, filepath.Join(w.projects, "p", fresh+".jsonl"), []byte(`{"type":"mode","mode":"default"}`+"\n"))
	contains(t, w.ok("show", fresh), "no events in the transcript")
}

// A second trace of a fact the reader's rule missed is said on the view, so
// drift shows without anyone running check.
func TestViewSaysWhenTheReaderMayHaveMissedSomething(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("abab1212")
	fixture.New().Bash(`sh -c "git commit -m x"`, "[main 1a2b3c4] x").Write(t, w.projects, "p", id)
	w.panes = []panes.Pane{{ID: "%9", Where: "x:1.1", SessionID: id}}
	want := "the reader may have missed 1 × commit (claude-steps check)"
	contains(t, w.ok("show", id), want)
	contains(t, w.ok("board"), "x:1.1  ! app", "["+want+"]")
}

func TestRepeatedLinesAreOneLine(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("cdcd3434")
	tr := fixture.New()
	for range 4 {
		tr.Slash("pl-handle-code-review", "7619", "/repo/.claude/skills/pl-handle-code-review")
	}
	tr.Bash(`git commit -m "fix"`, "")
	tr.Slash("pl-handle-code-review", "7619", "/repo/.claude/skills/pl-handle-code-review")
	tr.Write(t, w.projects, "p", id)
	out := w.ok("show", id, "--all")
	if strings.Count(out, "/pl-handle-code-review") != 2 {
		t.Errorf("want one line for the run of four and one for the fifth:\n%s", out)
	}
	contains(t, out, "/pl-handle-code-review  7619  (4 times)")

	// The same among the steps, where the commit between is a count line.
	tr = fixture.New()
	for range 3 {
		tr.Slash("review", "codex", "/home/u/.claude/skills/review")
	}
	tr.Bash(`git commit -m "fix"`, "")
	tr.Slash("review", "codex", "/home/u/.claude/skills/review")
	tr.Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), "/review  codex\n                1 commit\n  review   3h   /review  codex  (3 times)\n")
}

var escape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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

// A round dispatched here with no collect in the transcript is listed under
// the labels, whatever ran after it and whether or not a label lists it. The
// label's own row would hide it behind a later round.
func TestRoundsWithNoCollectSeen(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("ababab12")
	tr := fixture.New()
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash("envoy run spike-r1 --with codex --prompt-file /tmp/s.md", "Command running in background")
	tr.At(fixture.Start.Add(time.Hour))
	tr.Bash("envoy run review-r2 --with codex --prompt-file /tmp/r2.md", "Command running in background")
	tr.Bash("envoy collect review-r2", fmt.Sprintf(collected, "review-r2", "ok"))
	tr.BashError("envoy run review-r3 --with codex --prompt-file /tmp/r3.md", "envoy: no such voice")
	tr.Write(t, w.projects, "p", id)
	w.panes = []panes.Pane{{ID: "%9", Where: "x:1.1", SessionID: id}}

	out := w.ok("show", id)
	contains(t, out,
		"review    2 hours ago   0 commits since   review-r2 collected 2 hours ago\n",
		"no collect seen   spike-r1    3 hours ago\n                  review-r1   3 hours ago\nnotes",
		// A run that returned an error is not waiting for a collect.
		"  review   2h   review-r3  run returned an error\n  review   2h   review-r2\n  review   3h   review-r1  no collect seen\n",
	)
	// The round no label lists is not a step; the count tells the reader
	// there is more.
	lacks(t, out, "spike-r1  dispatched")
	contains(t, w.ok("show", id, "--all"), "           3h   spike-r1  dispatched\n")
	contains(t, w.ok("board"), "x:1.1  app      2h +0   ·       ·        3h ×2")

	// The newest three, and how many more.
	many := fixture.New()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		many.Bash("envoy run job-"+name+" --with codex --prompt-file /tmp/p.md", "Command running in background")
	}
	many.Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), "no collect seen   job-e    3 hours ago\n                  job-d    3 hours ago\n                  job-c    3 hours ago\n                  2 more\n")
}

// The commits around the steps are counted wherever they fall: after the
// newest step, between two, before the oldest, and with no step at all.
func TestStepsCountTheCommitsAroundThem(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("cdcdcd34")
	tr := fixture.New()
	tr.Bash(`git commit -m "one"`, "")
	tr.SkillCall("pl-loopy-verify", "", "p", false)
	tr.Bash(`git commit -m "two"`, "")
	tr.Bash(`git commit -m "three"`, "")
	tr.Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), `
steps
                2 commits
  verify   3h   skill pl-loopy-verify
                1 commit

4 rows in the full history (show --all)
`)

	tr = fixture.New()
	tr.Bash(`git commit -m "one"`, "")
	tr.Slash("unlabelled", "", "/home/u/.claude/skills/unlabelled")
	tr.Bash(`git commit -m "two"`, "")
	tr.Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), "\nsteps\n  2 commits\n\n3 rows in the full history (show --all)\n")

	fixture.New().Slash("unlabelled", "", "/home/u/.claude/skills/unlabelled").Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), "\nsteps\n  none\n\n1 row in the full history (show --all)\n")
}

// Labels are not exclusive. A step under two is one row with both names,
// and the commits beside it are counted once.
func TestAStepUnderTwoLabels(t *testing.T) {
	w := newWorld(t)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), []byte(`
[[label]]
name = "review"
skills = ["review"]
count_commits = true

[[label]]
name = "docs"
skills = ["update-docs", "review"]
color = "yellow"
`))
	id := fixture.ID("efefef56")
	tr := fixture.New()
	tr.Slash("review", "codex", "/home/u/.claude/skills/review")
	tr.Bash(`git commit -m "fix"`, "")
	tr.Prompt("next, the review skill and then update-docs")
	tr.SkillCall("review", "", "p", true)
	tr.Write(t, w.projects, "p", id)
	out := w.ok("show", id)
	contains(t, out, `
steps
  review docs   3h   skill review  failed to load
  review docs   3h   you: "next, the review skill and then update-docs"
                     1 commit
  review docs   3h   /review  codex
`)
	if strings.Count(out, "1 commit\n") != 1 {
		t.Errorf("the commit is counted once:\n%s", out)
	}
	w.env = map[string]string{"CLICOLOR_FORCE": "1"}
	contains(t, w.ok("show", id),
		"  \x1b[34mreview\x1b[0m \x1b[33mdocs\x1b[0m   3h   \x1b[31mskill review  failed to load\x1b[0m\n",
		"\x1b[33mdocs\x1b[0m     3 hours ago",
	)
}

// With no label configured there are no steps to pick, and the view is the
// whole timeline.
func TestNoLabelsShowTheHistory(t *testing.T) {
	w := newWorld(t)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), nil)
	out := w.ok("show", "%1")
	contains(t, out, "\nhistory\n", "commit  docs: the stories on the local rig")
	lacks(t, out, "steps\n", "in the full history")
	if got := columns.Split(strings.SplitN(w.ok("board"), "\n", 2)[0], -1); !slices.Equal(got, []string{"pane", "session", "no collect", "PR", "note"}) {
		t.Errorf("header: %q", got)
	}
}

// columnOf is the screen column text starts at in line, counting a CJK
// character as two.
func columnOf(t *testing.T, line, text string) int {
	t.Helper()
	i := strings.Index(line, text)
	if i < 0 {
		t.Fatalf("no %q in %q", text, line)
	}
	return screen.StringWidth(line[:i])
}

var screen = &runewidth.Condition{StrictEmojiNeutral: true}

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

// A session view cuts each event to the width it is given, so a row is one
// line of the preview, and puts the header on two lines when one is too long.
func TestTheSessionViewFitsTheWidth(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("9a9a9a9a")
	tr := fixture.New().Title("A session with a long request")
	tr.Slash("review", strings.Repeat("codex full review of the whole branch ", 6), "/home/u/.claude/skills/review")
	tr.Bash("gh pr create --fill", "https://github.com/acme/app/pull/12")
	tr.PRLink("acme/app", 12)
	tr.Write(t, w.projects, "p", id)
	w.env = map[string]string{"COLUMNS": "90"}
	for _, args := range [][]string{{"show", id}, {"show", id, "--all"}} {
		out := w.ok(args...)
		for _, row := range strings.Split(out, "\n") {
			if screen.StringWidth(row) > 90 {
				t.Errorf("%v: a row takes %d columns:\n%s", args, screen.StringWidth(row), row)
			}
		}
		contains(t, out, strings.Repeat("─", 90)+"\n", "…\n")
	}
	// The pull requests take lines of their own once the header does not fit
	// on one, newest first, and a link is never cut.
	w.env = map[string]string{"COLUMNS": "40"}
	contains(t, w.ok("show", id), "/work/app  feat/thing\nPR #12 opened here  https://github.com/acme/app/pull/12\n"+strings.Repeat("─", 40)+"\n")
	tr.Compaction("manual", false, "summary")
	tr.Raw(fixture.Row{"type": "pr-link", "prNumber": 13, "prUrl": "https://github.com/acme/app/pull/13", "prRepository": "acme/app"})
	tr.Write(t, w.projects, "p", id)
	w.env = map[string]string{"COLUMNS": "100"}
	contains(t, w.ok("show", id), "/work/app  feat/thing   1 compaction, last 3 hours ago\n"+
		"PR #13 linked  https://github.com/acme/app/pull/13\n"+
		"PR #12 opened here  https://github.com/acme/app/pull/12\n"+strings.Repeat("─", 100)+"\n")
}

// Notes: obligations 11 and 12, and the session a note lands in.
func TestNote(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "skip", "verify,", "the", "spike", "covered", "it")
	w.now = w.now.Add(20 * time.Minute)
	w.ok("note", "aaaaaaaa", "second note")

	out := w.ok("show", worked)
	// Newest first under the labels, and again among the steps at the time
	// each was written.
	contains(t, out,
		"notes             now              second note\n                  20 minutes ago   skip verify, the spike covered it\n",
		"steps\n  note      now   second note\n  note      20m   skip verify, the spike covered it\n",
	)
	contains(t, w.ok("board"), "(2) second note")

	file := filepath.Join(w.state, "notes", worked+".jsonl")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"at":"2026-10-01T12:30:00Z","text":"skip verify, the spike covered it"}` + "\n" + `{"at":"2026-10-01T12:50:00Z","text":"second note"}` + "\n"
	if string(data) != want {
		t.Errorf("notes file:\n%s", data)
	}

	// The note text is opaque after "--", even when it reads like an option
	// (review r1).
	w.ok("note", worked, "--", "--help")
	contains(t, w.ok("show", worked), "notes             now              --help\n")

	// A damaged line is reported and the notes around it are shown.
	os.WriteFile(file, append(data, []byte("{broken\n")...), 0o644)
	contains(t, w.ok("show", worked), "second note", "1 note line could not be read")

	for _, args := range [][]string{
		{"note", "%1"},                         // no text
		{"note", "%1", "  "},                   // empty text
		{"note", "%6", "x"},                    // a pane with no session
		{"note", "%77", "x"},                   // no such pane
		{"note", fixture.ID("99999999"), "x"},  // a session this machine has never seen
		{"note", "9999", "x"},                  // too short to be a prefix
		{"note", "../../etc/passwd", "x"},      // not an id
		{"note", "%1", "--to-session", "text"}, // an option the command does not have
	} {
		if _, errb, code := w.run(args...); code == 0 || errb == "" {
			t.Errorf("claude-steps %q: exit %d, stderr %q", args, code, errb)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(w.state, "notes"))
	if len(entries) != 1 {
		t.Errorf("a refused note left a file behind: %v", entries)
	}

	// A notes file that cannot be read is said, and a note that cannot be
	// written fails.
	os.Remove(file)
	os.Mkdir(file, 0o755)
	contains(t, w.ok("show", worked), "cannot read the notes for "+worked)
	if _, errb, code := w.run("note", worked, "lost"); code == 0 || !strings.Contains(errb, "the note was not saved") {
		t.Errorf("a failed write: exit %d, %q", code, errb)
	}
}

// A live session with no transcript yet, as before its first prompt, is
// still a session: the popup addresses it by id, and its first note lands.
func TestLiveSessionWithNoTranscriptTakesANote(t *testing.T) {
	w := newWorld(t)
	w.ok("note", gone, "noted before the first prompt")
	contains(t, w.ok("show", gone), "no transcript", "noted before the first prompt")
	contains(t, w.ok("show", "cccccccc"), "noted before the first prompt")
}

// Help needs no configuration: a broken file cannot hide the usage.
func TestHelpNeedsNoConfiguration(t *testing.T) {
	w := newWorld(t)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), []byte("[[label]]\nskils = 1\n"))
	if out, errb, code := w.run("show", "--help"); code != 0 || !strings.Contains(out, "claude-steps show") {
		t.Errorf("show --help with a broken configuration: exit %d, %q, %q", code, out, errb)
	}
	if _, errb, code := w.run("show", "%1"); code == 0 || !strings.Contains(errb, "skils") {
		t.Errorf("a broken configuration was not reported: exit %d, %q", code, errb)
	}
}

// A dispatch that failed says so, and a fact with no time says that rather
// than borrowing one.
func TestFailedRunAndUnknownTimeAreSaid(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("abcdabcd")
	tr := fixture.New()
	tr.Raw(fixture.Row{"type": "pr-link", "prNumber": 4, "prUrl": "https://github.com/acme/app/pull/4", "prRepository": "acme/app"})
	tr.BashError("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Exit code 2\nenvoy: no such voice")
	tr.Write(t, w.projects, "-work-app", id)
	contains(t, w.ok("show", id), "  review   3h   review-r1  run returned an error\n")
	// A row with no time is the oldest the history can place.
	contains(t, w.ok("show", id, "--all"), "  review   3h   review-r1  run returned an error\n           ?    PR #4 linked  acme/app\n")
}

// Notes another machine kept for the session are merged from stdin.
func TestImportNotes(t *testing.T) {
	w := newWorld(t)
	w.ok("note", worked, "written here")
	var out, errb bytes.Buffer
	a := &app{stdin: strings.NewReader(`{"at":"2026-10-01T09:00:00Z","text":"from the laptop"}` + "\n"), stdout: &out, stderr: &errb,
		now: func() time.Time { return w.now }, panes: func() ([]panes.Pane, error) { return w.panes, nil }, getenv: func(string) string { return "" }}
	if code := a.run([]string{"import-notes", worked}); code != 0 || out.String() != "1 notes added\n" {
		t.Fatalf("exit %d, %q, %q", code, out.String(), errb.String())
	}
	contains(t, w.ok("show", worked), "notes             now           written here\n                  3 hours ago   from the laptop\n")
	if _, _, code := w.run("import-notes", "aaaaaaaa"); code == 0 {
		t.Error("import-notes takes a full session id")
	}
}

// Obligation 13, first half.
func TestAmbiguousPrefixIsRefused(t *testing.T) {
	w := newWorld(t)
	twin := "aaaaaaaa-9999-4999-8999-999999999999"
	fixture.New().Prompt("x").Write(t, w.projects, "-work-twin", twin)
	_, errb, code := w.run("show", "aaaaaaaa")
	if code == 0 {
		t.Fatal("an ambiguous prefix was accepted")
	}
	contains(t, errb, worked, twin)
	contains(t, w.ok("show", "aaaaaaaa-1"), "The calendar walks days once")
}

func TestWithoutTmuxOrASession(t *testing.T) {
	w := newWorld(t)
	if _, errb, code := w.run("show", "%6"); code == 0 || !strings.Contains(errb, "pane %6 has no Claude session") {
		t.Errorf("show on a pane with no session: exit %d, %q", code, errb)
	}
	if _, errb, code := w.run("show"); code == 0 || !strings.Contains(errb, "outside tmux") {
		t.Errorf("show with no pane: exit %d, %q", code, errb)
	}
	w.panesErr = panes.ErrNoServer
	if out, errb, code := w.run("board"); code == 0 || out != "" || !strings.Contains(errb, "no tmux server") {
		t.Errorf("board with no server: exit %d, %q, %q", code, out, errb)
	}
	w.panesErr, w.panes = nil, nil
	if out, errb, code := w.run("board"); code != 0 || out != "" || !strings.Contains(errb, "no tmux pane runs a Claude session") {
		t.Errorf("board with no Claude pane: exit %d, %q, %q", code, out, errb)
	}
	if out, _, code := w.run("--help"); code != 0 || !strings.Contains(out, "/clear") || !strings.Contains(out, "forked session") {
		t.Errorf("--help should say what starts a new session id:\n%s", out)
	}
	if _, _, code := w.run("frobnicate"); code != 2 {
		t.Errorf("unknown command: exit %d", code)
	}
}

// Obligation 19.
func TestCheck(t *testing.T) {
	w := newWorld(t)
	// A line that does not decode is drift too: Claude Code writes whole
	// lines, so one it cannot have written means the shape moved (review r1).
	os.Remove(filepath.Join(w.projects, "-work-app", garbled+".jsonl"))
	w.now = time.Now()
	out, _, code := w.run("check")
	if code == 0 || !strings.Contains(out, "1 read in part, 1 lines could not be decoded") {
		t.Fatalf("a transcript with an undecodable line should fail check:\n%s", out)
	}
	os.Remove(filepath.Join(w.projects, "-work-app", torn+".jsonl"))
	out, errb, code := w.run("check")
	if code != 0 {
		t.Fatalf("a consistent set should pass: %s\n%s", errb, out)
	}
	contains(t, out, "2 transcripts changed in the last 7 days", "pull request", "no drift")

	drifted := fixture.ID("abab1212")
	fixture.New().Bash("gh pr create --fill", "https://github.com/acme/app/pull/9").Write(t, w.projects, "p", drifted)
	out, _, code = w.run("check")
	if code == 0 {
		t.Fatalf("a created pull request with no link row is drift:\n%s", out)
	}
	contains(t, out, "missed in abab1212", `the reader missed 1 of 2 for "pull request"`)
	os.Remove(filepath.Join(w.projects, "p", drifted+".jsonl"))

	// One miss in ten second traces is within tolerance; the reader is not
	// asked to beat Claude Code's own rate of dropped link rows.
	tolerated := fixture.New()
	for n := 1; n <= 10; n++ {
		tolerated.Bash("gh pr create --fill", fmt.Sprintf("https://github.com/acme/app/pull/%d", n))
		if n > 1 {
			tolerated.PRLink("acme/app", n)
		}
	}
	tolerated.Write(t, w.projects, "p", drifted)
	if out, _, code := w.run("check"); code != 0 {
		t.Errorf("one miss in ten should pass:\n%s", out)
	}
	os.Remove(filepath.Join(w.projects, "p", drifted+".jsonl"))

	// A transcript older than the window is not read.
	old := time.Now().Add(-8 * 24 * time.Hour)
	stale := fixture.New().Bash("gh pr create --fill", "https://github.com/acme/app/pull/9").Write(t, w.projects, "p", fixture.ID("cdcd3434"))
	os.Chtimes(stale, old, old)
	if out, _, code := w.run("check"); code != 0 {
		t.Errorf("a transcript outside the window was read:\n%s", out)
	}

	// When no transcript holds a conversation row, the row types moved.
	silentHome := t.TempDir()
	silent := filepath.Join(silentHome, ".claude", "projects", "p")
	fixture.WriteFile(t, filepath.Join(silent, fixture.ID("abcd0001")+".jsonl"), []byte(`{"type":"turn","text":"x"}`+"\n"))
	t.Setenv("HOME", silentHome)
	if out, _, code := w.run("check"); code == 0 || !strings.Contains(out, "no transcript holds a conversation row") {
		t.Errorf("a week with no conversation rows should fail:\n%s", out)
	}
	t.Setenv("HOME", w.home)

	fixture.WriteFile(t, filepath.Join(w.projects, "p", garbled+".jsonl"), []byte("not json\n"))
	out, _, code = w.run("check")
	if code == 0 || !strings.Contains(out, "unreadable: "+garbled) {
		t.Errorf("an unreadable transcript should fail check:\n%s", out)
	}
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(root, path)
		files[rel] = fmt.Sprintf("%x %s", sha256.Sum256(data), info.ModTime().Format(time.RFC3339Nano))
		return nil
	})
	return files
}

// Obligation 14: nothing under the home changes except the notes.
func TestOnlyTheNotesAreWritten(t *testing.T) {
	w := newWorld(t)
	before := snapshot(t, w.home)
	w.tmuxPane = "%1"
	for _, args := range [][]string{
		{"board"}, {"board", "--ids"}, {"board", "--json"}, {"show"}, {"show", "%2"}, {"show", "%3"}, {"show", "%4", "--json"},
		{"show", "%5"}, {"show", "aaaaaaaa"}, {"check"}, {"note", "%1", "one"}, {"note", worked, "two"}, {"note", "%3", "three"}, {"--help"},
	} {
		w.run(args...)
	}
	after := snapshot(t, w.home)
	notes := filepath.Join(".local", "state", "claude-steps", "notes")
	for path, sum := range after {
		if was, existed := before[path]; existed && was != sum {
			t.Errorf("%s was modified", path)
		} else if !existed && filepath.Dir(path) != notes {
			t.Errorf("%s was created outside the notes directory", path)
		}
	}
	for path := range before {
		if _, kept := after[path]; !kept {
			t.Errorf("%s was removed", path)
		}
	}
	if len(after) != len(before)+2 {
		t.Errorf("want two notes files, got %d new files", len(after)-len(before))
	}
}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	return strings.Fields(string(out))
}

// Obligation 15: the binary cannot reach a network, so it cannot call a model.
func TestNoNetworkInTheBinary(t *testing.T) {
	for _, pkg := range goList(t, "-deps", ".") {
		if pkg == "net" || strings.HasPrefix(pkg, "net/") || strings.HasPrefix(pkg, "crypto/tls") {
			t.Errorf("the binary links %s", pkg)
		}
	}
}

// The tool has no handle on a session. Its only child process is tmux
// list-panes, which reads; and one package knows where transcripts live
// (obligation 13, second half).
func TestOneReaderAndOneReadOnlyProcess(t *testing.T) {
	root := filepath.Join("..", "..")
	var execUsers, transcriptReaders []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, imp := range file.Imports {
			switch strings.Trim(imp.Path.Value, `"`) {
			case "os/exec", "syscall", "plugin":
				execUsers = append(execUsers, rel)
			}
		}
		if bytes.Contains(src, []byte("ProjectsDir")) {
			transcriptReaders = append(transcriptReaders, rel)
		}
		return nil
	})
	if got := strings.Join(execUsers, " "); got != filepath.Join("internal", "panes", "panes.go") {
		t.Errorf("packages that can start a process: %s", got)
	}
	if got := strings.Join(transcriptReaders, " "); got != filepath.Join("internal", "config", "config.go")+" "+filepath.Join("internal", "record", "load.go") {
		t.Errorf("files that know where transcripts live: %s", got)
	}

	src, _ := os.ReadFile(filepath.Join(root, "internal", "panes", "panes.go"))
	calls := regexp.MustCompile(`exec\.Command\(([^\n]*)\)\.`).FindAllStringSubmatch(string(src), -1)
	if len(calls) != 1 || calls[0][1] != `"tmux", "list-panes", "-a", "-F", format` {
		t.Errorf("tmux is called with: %q", calls)
	}
}
