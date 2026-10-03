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
	"strings"
	"testing"
	"time"

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
	a := &app{stdout: &out, stderr: &errb, now: func() time.Time { return w.now },
		panes: func() ([]panes.Pane, error) { return w.panes, w.panesErr },
		getenv: func(k string) string {
			if k == "TMUX_PANE" {
				return w.tmuxPane
			}
			return ""
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
	contains(t, out,
		"The calendar walks days once   aaaaaaaa   work:1.1",
		"/work/app  feat/thing",
		"PR #7145 opened here  acme/app",
		"1 compaction, last 2 hours ago",
		"3 hours ago      /review  codex full review",
		"3 hours ago        review-r1  dispatched\n",
		"3 hours ago        review-r1  collected\n",
		"3 hours ago      commit  the calendar walks days once (review r1)",
		"2 hours ago        review-r2  collected, envoy said partial",
		"2 hours ago      compaction (manual)",
		"15 minutes ago   skill pl-loopy-verify  local spikes",
		"notes\n  none",
	)
	// The label lines: the latest review event is the second dispatch, and
	// one commit was made since.
	contains(t, out,
		"review    2 hours ago      1 commit since    review-r2 collected 2 hours ago, envoy said partial",
		"verify    15 minutes ago   0 commits since   skill pl-loopy-verify local spikes",
		"prompts   12 minutes ago",
	)

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

// A round collected long after its dispatch shows each at its own time;
// the label still dates the round from the dispatch, the code the reviewer
// read.
func TestCollectIsShownWhenItHappened(t *testing.T) {
	w := newWorld(t)
	id := fixture.ID("ffffffff")
	tr := fixture.New()
	tr.Bash("envoy run review-r7 --with codex --prompt-file /tmp/r7.md", "Command running in background")
	tr.At(w.now.Add(-16 * time.Minute)) // the result row is a minute after the call
	tr.Bash("envoy collect review-r7", fmt.Sprintf(collected, "review-r7", "partial"))
	tr.Write(t, w.projects, "-work-app", id)
	out := w.ok("show", id)
	contains(t, out,
		"3 hours ago        review-r7  dispatched\n",
		"15 minutes ago     review-r7  collected, envoy said partial\n",
		"3 hours ago   0 commits since   review-r7 collected 15 minutes ago, envoy said partial\n",
	)
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

// A label's cell is a date, never a judgement. This is the whole grammar.
var cell = regexp.MustCompile(`^(·|(read )?(now|\d+ (second|minute|hour|day|week|month|year)s? ago)( \+\d+)?|named (now|\d+ (second|minute|hour|day|week|month|year)s? ago))$`)

// Obligations 5, 10 and 12 on the board.
func TestBoard(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%3", "transcript deleted, the PR was merged")
	out := w.ok("board")
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 6 {
		t.Fatalf("want a header and five panes (the pane with a malformed id is not a session):\n%s", out)
	}
	contains(t, rows[0], "pane", "session", "review", "verify", "prompts", "PR", "compactions", "note")
	contains(t, rows[1], "work:1.1", "The calendar walks days once", "2 hours ago +1", "15 minutes ago +0", "read 12 minutes ago", "#7145")
	contains(t, rows[2], "work:1.2", "named 3 hours ago")
	contains(t, rows[3], "work:2.1", "no transcript", "transcript deleted, the PR was merged")
	contains(t, rows[4], "work:2.2", "transcript unreadable")
	contains(t, rows[5], "work:3.1", "[1 line could not be read]")

	// Every label cell of every row fits the grammar.
	idRows := strings.Split(strings.TrimRight(w.ok("board", "--ids"), "\n"), "\n")
	header := regexp.MustCompile(` {3,}`).Split(strings.Split(idRows[0], "\t")[2], -1)
	for _, i := range []int{1, 2, 5} {
		fields := strings.Split(idRows[i], "\t")
		cells := regexp.MustCompile(` {3,}`).Split(fields[2], -1)
		for col := 2; col < 5; col++ {
			if !cell.MatchString(cells[col]) {
				t.Errorf("row %d, label %s: cell %q is not a date", i, header[col], cells[col])
			}
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
	for _, args := range [][]string{{"board"}, {"show", "%1"}, {"show", "%3"}, {"show", "%4"}, {"show", "%5"}} {
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
	contains(t, missing, "session cccccccc   cccccccc   work:2.1", "no transcript", "notes\n  now   kept past the transcript")
	unreadable := w.ok("show", "%4")
	contains(t, unreadable, "transcript unreadable", filepath.Join("-work-app", garbled+".jsonl"), "notes\n  none")
	for _, out := range []string{missing, unreadable} {
		lacks(t, out, "no events in the transcript", " ago")
	}

	partial := w.ok("show", "%5")
	contains(t, partial, "/review", "1 line could not be read")

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
	contains(t, w.ok("board"), "["+want+"]")
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
	out := w.ok("show", id)
	if strings.Count(out, "/pl-handle-code-review") != 2 {
		t.Errorf("want one line for the run of four and one for the fifth:\n%s", out)
	}
	contains(t, out, "/pl-handle-code-review  7619  (4 times)")
}

// Notes: obligations 11 and 12, and the session a note lands in.
func TestNote(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "skip", "verify,", "the", "spike", "covered", "it")
	w.now = w.now.Add(20 * time.Minute)
	w.ok("note", "aaaaaaaa", "second note")

	out := w.ok("show", worked)
	contains(t, out,
		"20 minutes ago   note: skip verify, the spike covered it",
		"notes\n  20 minutes ago   skip verify, the spike covered it\n  now              second note",
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
	contains(t, w.ok("show", worked), "note: --help")

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
	contains(t, w.ok("show", id),
		"at an unknown time   PR #4 linked  acme/app",
		"review-r1  run returned an error",
	)
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
	contains(t, w.ok("show", worked), "notes\n  3 hours ago   from the laptop\n  now           written here")
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
