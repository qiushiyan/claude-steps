package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/fixture"
	"github.com/qiushiyan/claude-steps/internal/panes"
)

// Obligations 3, 4 and 17 as the user reads them.
func TestShow(t *testing.T) {
	w := newWorld(t)
	out := w.ok("show", "%1")
	// What the session is, on two lines and under a rule: the second says
	// where, and when the transcript last had a message.
	contains(t, out,
		"The calendar walks days once   aaaaaaaa   work:1.1\n"+
			"/work/app   feat/thing   1 compaction, last 2 hours ago   last message 11 minutes ago   PR #7145 opened here  https://github.com/acme/app/pull/7145\n"+
			strings.Repeat("─", 72)+"\n",
	)
	// The label lines come first: the latest review event is the second
	// dispatch, and one commit was made since. How a round was dispatched and
	// collected is the history's to say.
	contains(t, out,
		"review    2 hours ago      1 commit since    review-r2 envoy said partial\n"+
			"verify    15 minutes ago   0 commits since   skill pl-loopy-verify local spikes\n"+
			"prompts   12 minutes ago                     read skills/prompt-engineering/SKILL.md\n"+
			"\nnotes   none\n",
	)
	// The steps, newest first: what is under a label, a round as one line at
	// its dispatch with the command that asked for it, and the commits between
	// as a count.
	contains(t, out, `
steps
  prompts   12m   read skills/prompt-engineering/SKILL.md
  verify    15m   skill pl-loopy-verify  local spikes
                  1 commit
  review    2h    review-r2  envoy said partial
                  1 commit
  review    3h    /review codex full review → review-r1

11 rows in the full history (show --all)
`)
	lacks(t, out, "compaction (manual)", "commit  ", "history\n", "dispatched", "collected", "no collect seen")

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
// dispatch as its label is: the code the reviewer read. The full history says
// when the collect happened.
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
		"3 hours ago   0 commits since   review-r7 envoy said partial\n",
	)
	contains(t, w.ok("show", id, "--all"),
		"  review   15m   review-r7  collected, envoy said partial\n"+
			"  review   3h    review-r7  dispatched\n",
	)
}

// Under a label that lists rounds a step is a round. The skill's runs since
// the label's last round are the round's, and the latest command typed among
// them is on its line; a run no round took keeps a line of its own, and a
// round that followed another has nothing to carry.
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
  review   3h   review-r0
  verify   3h   skill pl-loopy-verify  spikes
  review   3h   review-r2
                1 commit
  review   3h   /review codex full → review-r1
                1 commit

`)
	lacks(t, out, "goal", "no dispatch seen", "no collect seen")
	// The history keeps every line the steps folded.
	all := w.ok("show", id, "--all")
	contains(t, all, "review-r1  collected\n", "review-r1  dispatched\n", "/review  codex full\n", "/review  goal\n")
	lacks(t, all, "no dispatch seen")

	// A skill run after the last round is a line of its own.
	tr.Slash("review", "one more", "/home/u/.claude/skills/review")
	tr.Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), "\nsteps\n  review   3h   /review  one more\n  review   3h   review-r0\n")
}

// A request under a label is said on the step that answered it: the first
// run or round under that label made in the same prompt. Asked in one prompt
// and run in a later one, the two are lines of their own, and the same
// request sent twice before anything answered it is one.
func TestARequestIsSaidOnTheStepThatAnsweredIt(t *testing.T) {
	w := newWorld(t)
	projectSnippets(t, w)
	id := fixture.ID("babe1234")
	tr := fixture.New()
	tr.Prompt("<pasted_content id=\"1\">\n/review " + reviewVerify + "\n</pasted_content>")
	tr.SkillCall("review", "codex full review", "p1", false)
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.SkillCall("pl-loopy-verify", "spikes on the local rig", "p1", false)
	tr.Bash(`git commit -q -m "fix"`, "")
	tr.Prompt("now follow the prompt-engineering skill")
	tr.Read("/home/u/.claude/skills/prompt-engineering/SKILL.md", false)
	tr.Prompt("have we run pl-loopy-verify yet?")
	tr.Prompt("go on")
	tr.SkillCall("pl-loopy-verify", "again", "p4", false)
	tr.Prompt(promptCheck)
	tr.Prompt(promptCheck)
	tr.Write(t, w.projects, "p", id)
	out := w.ok("show", id)
	contains(t, out, `
steps
  prompts   3h   pasted app-prompt-check
  verify    3h   skill pl-loopy-verify  again
  verify    3h   you: "have we run pl-loopy-verify yet?"
  prompts   3h   you: "now follow the prompt-engineerin…" → read skills/prompt-engineering/SKILL.md
                 1 commit
  verify    3h   pasted app-review-verify → skill pl-loopy-verify  spikes on the local rig
  review    3h   pasted app-review-verify → review-r1

`)
	lacks(t, out, "skill review", "times)")
	// The full history keeps each at its own time.
	contains(t, w.ok("show", id, "--all"), "  review verify   3h   pasted app-review-verify\n", "  prompts         3h   pasted app-prompt-check  (2 times)\n")

	// Narrow, the request gives way to what ran: the round's name and the
	// skill stay, and a request with too little room is left out.
	w.env = map[string]string{"COLUMNS": "50"}
	narrow := w.ok("show", id, "--no-head")
	contains(t, narrow, " → review-r1\n", "  verify    3h   skill pl-loopy-verify  spikes on…\n")
	w.env = nil

	// A round takes the request made in its own prompt over one an earlier
	// run of the skill answered, and answers a request with no run between.
	// A run of the model's own, asked for by nothing, lends the round its
	// words.
	tr = fixture.New()
	tr.Prompt("now run the review skill on the branch")
	tr.SkillCall("review", "codex full review", "p1", false)
	tr.Prompt("<pasted_content id=\"2\">\n/review " + reviewVerify + "\n</pasted_content>")
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Prompt("<pasted_content id=\"3\">\n/review " + reviewVerify + " again\n</pasted_content>")
	tr.Bash("envoy run review-r2 --with codex --prompt-file /tmp/r2.md", "Command running in background")
	tr.Notification("review-r2 finished")
	tr.SkillCall("review", "codex, the fixes", "p3", false)
	tr.Bash("envoy run review-r3 --with codex --prompt-file /tmp/r3.md", "Command running in background")
	tr.Write(t, w.projects, "p", id)
	out = w.ok("show", id, "--no-head")
	contains(t, out, "  review   3h   review-r3  skill review  codex, the fixes\n"+
		"  review   3h   pasted app-review-verify → review-r2\n"+
		"  verify   3h   pasted app-review-verify\n"+
		"  review   3h   pasted app-review-verify → review-r1\n\n")
	lacks(t, out, "now run the review", "codex full review")

	// With no prompt marked as the user's, nothing says which prompt a run
	// answered, and a request and a run stay apart.
	tr = fixture.New()
	tr.Unsourced("run pl-loopy-verify now")
	tr.Unsourced("then something else entirely")
	tr.Read("/home/u/.claude/skills/pl-loopy-verify/SKILL.md", false)
	tr.Write(t, w.projects, "p", id)
	out = w.ok("show", id, "--no-head")
	contains(t, out, "  verify   3h   read skills/pl-loopy-verify/SKILL.md\n  verify   3h   you: \"run pl-loopy-verify now\"\n")
	lacks(t, out, "→")
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
	contains(t, out, "\nsteps\n  review   3h   review-r5\n  review   3h   review-r4\n\n")
	lacks(t, out, "dispatched", "no collect seen")
	contains(t, w.ok("show", id, "--all"), "review-r4  dispatched  (2 times)\n")
}

// A run that returned an error keeps a line of its own and takes no count
// from the dispatches around it. When envoy's job line shows the job was
// created, a collect by the name reads that round, the step says both, and
// the label counts it: a result came back. With no job line the run ran
// nothing, and the collect is a round dispatched somewhere else.
func TestARunThatReturnedAnError(t *testing.T) {
	const run = "envoy run review-r1 --with codex --prompt-file /tmp/p.md"
	w := newWorld(t)
	id := fixture.ID("a0a0a0a0")
	show := func(tr *fixture.Transcript, args ...string) string {
		tr.Write(t, w.projects, "p", id)
		return w.ok(append([]string{"show", id}, args...)...)
	}

	tr := fixture.New()
	tr.Bash(run, "Command running in background")
	tr.BashError(run, "envoy: unknown voice")
	tr.Bash(run, "Command running in background")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "ok"))
	contains(t, show(tr), "\nsteps\n  review   3h   review-r1\n  review   3h   review-r1  run returned an error\n\n")

	tr = fixture.New()
	tr.BashError(run, "job: /jobs/app-1/review-r1\nprovider: codex\nstatus: timeout — the turn hit its cap\n")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "timeout"))
	contains(t, show(tr),
		"review    3 hours ago   0 commits since   review-r1 run returned an error, envoy said timeout\n",
		"\nsteps\n  review   3h   review-r1  run returned an error, envoy said timeout\n\n",
	)
	contains(t, show(tr, "--all"), "  review   3h   review-r1  collected, envoy said timeout\n  review   3h   review-r1  run returned an error\n")
	w.env = map[string]string{"CLICOLOR_FORCE": "1"}
	contains(t, show(tr), "\x1b[31mreview-r1  run returned an error, envoy said timeout\x1b[0m\n")
	w.env = nil

	tr = fixture.New()
	tr.BashError(run, "envoy: unknown voice")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "partial"))
	contains(t, show(tr),
		"review    3 hours ago   0 commits since   review-r1 envoy said partial\n",
		"\nsteps\n  review   3h   review-r1  envoy said partial\n  review   3h   review-r1  run returned an error\n\n",
	)
}

// A round under two labels takes the skill runs before it under either, and
// says the latest command typed, not the one of the label the configuration
// lists first.
func TestARoundUnderTwoLabelsCarriesTheLatestRun(t *testing.T) {
	w := newWorld(t)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), []byte(`
[[label]]
name = "review"
skills = ["review"]
jobs = ["review-"]

[[label]]
name = "docs"
skills = ["update-docs"]
jobs = ["review-"]
`))
	id := fixture.ID("e0e0e0e0")
	tr := fixture.New().Slash("review", "older", "/home/u/.claude/skills/review")
	tr.Slash("update-docs", "newer", "/home/u/.claude/skills/update-docs")
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/p.md", "Command running in background")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "ok"))
	tr.Write(t, w.projects, "p", id)
	contains(t, w.ok("show", id), "\nsteps\n  review docs   3h   /update-docs newer → review-r1\n\n")
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

// Obligations 10 and 12 in the session view.
func TestUnreadTranscriptsSayWhy(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%3", "kept past the transcript")

	missing := w.ok("show", "%3")
	contains(t, missing, "session cccccccc   cccccccc   work:2.1\nno transcript\n", "notes   now   kept past the transcript")
	unreadable := w.ok("show", "%4")
	contains(t, unreadable, "transcript unreadable", filepath.Join("-work-app", garbled+".jsonl"), "notes   none")
	for _, out := range []string{missing, unreadable} {
		lacks(t, out, "no events in the transcript", " ago", "no collect seen")
	}
	lacks(t, unreadable, "steps")

	// The notes outlive the transcript, so its steps are the notes, each
	// whole however narrow the view.
	w.ok("note", "%3", "the transcript went with the worktree, and the review it held had come back clean before the merge")
	w.env = map[string]string{"COLUMNS": "44"}
	for _, args := range [][]string{{"show", "%3"}, {"show", "%3", "--no-head"}, {"show", "%3", "--all"}} {
		contains(t, w.ok(args...), "worktree, and the review it\n", "held had come back clean\n", "before the merge\n", "note   now   kept past the transcript\n")
	}
	if timeline := w.ok("show", "%3", "--no-head"); !strings.HasPrefix(timeline, "no transcript\n") {
		t.Errorf("the steps of a missing transcript do not say so first:\n%s", timeline)
	}
	w.env = nil

	// What the view may lack is said under the header, before the labels.
	partial := w.ok("show", "%5")
	contains(t, partial, "/work/app   feat/thing   last message 3 hours ago\n1 line could not be read\n─", "/review")

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

	// A paste sent as text and then as its own command is one request. Under
	// review the command answers it; under verify, where the command runs
	// nothing, it waits for a run.
	projectSnippets(t, w)
	tr = fixture.New()
	tr.Prompt("/review " + reviewVerify)
	tr.Slash("review", reviewVerify, "/home/u/.claude/skills/review")
	tr.Write(t, w.projects, "p", id)
	out = w.ok("show", id)
	contains(t, out, "\nsteps\n  review   3h   pasted app-review-verify → /review  full review. While you wait",
		"\n  verify   3h   pasted app-review-verify\n\n")
	lacks(t, out, "times)", "review verify")
}

// How a round was dispatched and collected is not a view's to say: a round is
// its name among the steps and in its label's row, and the full history
// keeps the dispatch and the collect.
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
		"review    2 hours ago   0 commits since   review-r2\n",
		"  review   2h   review-r3  run returned an error\n  review   2h   review-r2\n  review   3h   review-r1\n",
	)
	// The round no label lists is not a step; the count tells the reader
	// there is more.
	lacks(t, out, "spike-r1", "no collect seen", "dispatched", "collected")
	contains(t, w.ok("show", id, "--all"), "           3h   spike-r1  dispatched\n", "  review   2h   review-r2  collected\n")
	board := w.ok("board")
	contains(t, board, "x:1.1  app      2h +0   ·       ·        ·\n")
	lacks(t, board, "no collect", "×")
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
jobs = ["review-"]
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
	// A run under two labels that a round of one takes is still a step under
	// the other, and the label's row there says it.
	w.env = nil
	tr = fixture.New()
	tr.Read("/home/u/.claude/skills/review/SKILL.md", false)
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Write(t, w.projects, "p", id)
	out = w.ok("show", id)
	contains(t, out, "\nsteps\n  review   3h   review-r1\n  docs     3h   read skills/review/SKILL.md\n\n",
		"docs     3 hours ago                     read skills/review/SKILL.md\n")
}

// With no label configured there are no steps to pick, and the view is the
// whole timeline.
func TestNoLabelsShowTheHistory(t *testing.T) {
	w := newWorld(t)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), nil)
	out := w.ok("show", "%1")
	contains(t, out, "\nhistory\n", "commit  docs: the stories on the local rig")
	lacks(t, out, "steps\n", "in the full history")
	if got := columns.Split(strings.SplitN(w.ok("board"), "\n", 2)[0], -1); !slices.Equal(got, []string{"pane", "session", "PR", "note"}) {
		t.Errorf("header: %q", got)
	}
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
	// on one, newest first, and a link is never cut: one with no room beside
	// its name takes a line of its own.
	w.env = map[string]string{"COLUMNS": "40"}
	contains(t, w.ok("show", id), "/work/app   feat/thing\nlast message 3 hours ago\nPR #12 opened here\nhttps://github.com/acme/app/pull/12\n"+strings.Repeat("─", 40)+"\n")
	tr.Compaction("manual", false, "summary")
	tr.Raw(fixture.Row{"type": "pr-link", "prNumber": 13, "prUrl": "https://github.com/acme/app/pull/13", "prRepository": "acme/app"})
	tr.Write(t, w.projects, "p", id)
	w.env = map[string]string{"COLUMNS": "100"}
	contains(t, w.ok("show", id), "/work/app   feat/thing   1 compaction, last 3 hours ago   last message 3 hours ago\n"+
		"PR #13 linked  https://github.com/acme/app/pull/13\n"+
		"PR #12 opened here  https://github.com/acme/app/pull/12\n"+strings.Repeat("─", 100)+"\n")
}

// A popup shows a session's head beside its timeline, so each prints alone:
// the head is what the view holds before its steps, and the timeline is the
// steps, or the history, with no heading.
func TestTheHeadAndTheTimelineApart(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "a note")
	for heading, flags := range map[string][]string{"steps": nil, "history": {"--all"}} {
		show := func(more ...string) string { return w.ok(slices.Concat([]string{"show", "%1"}, flags, more)...) }
		if whole, head, timeline := show(), show("--head"), show("--no-head"); whole != head+"\n"+heading+"\n"+timeline {
			t.Errorf("%v: the view is not its head and its timeline:\n%s\n--- head\n%s\n--- timeline\n%s", flags, whole, head, timeline)
		}
	}
	contains(t, w.ok("show", "%1", "--no-head"), "  note      now   a note\n", "\n12 rows in the full history (show --all)\n")

	// The head lists the latest notes, and with --all every one.
	for _, text := range []string{"second", "third", "fourth"} {
		w.ok("note", "%1", text)
	}
	contains(t, w.ok("show", "%1", "--head"), "notes   now   fourth\n        now   third\n        now   second\n        1 earlier\n")
	all := w.ok("show", "%1", "--head", "--all")
	contains(t, all, "now   a note\n")
	lacks(t, all, "earlier")

	// A transcript that cannot be read says so in the timeline's place, and
	// the head is the whole view.
	if timeline := w.ok("show", "%3", "--no-head"); timeline != "no transcript\n" {
		t.Errorf("a missing transcript's timeline: %q", timeline)
	}
	if head, whole := w.ok("show", "%4", "--head"), w.ok("show", "%4"); head != whole {
		t.Errorf("an unreadable transcript's head is not its view:\n%s\n%s", head, whole)
	}

	for _, args := range [][]string{{"show", "%1", "--head", "--no-head"}, {"show", "%1", "--json", "--head"}, {"show", "%1", "--json", "--no-head"}} {
		if _, _, code := w.run(args...); code == 0 {
			t.Errorf("%v was not refused", args)
		}
	}
}

// Beside the timeline the head is narrow: a row gives its time as a cell
// does, the title, an event's text and a note are cut, and an item that does
// not fit a line starts the next. No row runs past the width.
func TestANarrowHead(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "the spike covered the verify pass, so skip it until the importer lands on the new schema")
	w.env = map[string]string{"COLUMNS": "48"}
	out := w.ok("show", "%1", "--head")
	for _, row := range strings.Split(out, "\n") {
		if screen.StringWidth(row) > 48 {
			t.Errorf("a row takes %d columns:\n%s", screen.StringWidth(row), row)
		}
	}
	contains(t, out,
		"The calendar walks days once   aaaaaaaa\nwork:1.1\n/work/app   feat/thing\n1 compaction, last 2 hours ago\nlast message 11 minutes ago\n"+
			"PR #7145 opened here\nhttps://github.com/acme/app/pull/7145\n"+strings.Repeat("─", 48)+"\n",
		"review    2h    +1   review-r2 envoy said parti…\n"+
			"verify    15m   +0   skill pl-loopy-verify loca…\n"+
			"prompts   12m        read skills/prompt-enginee…\n",
		"notes   now   the spike covered the verify pass…\n",
	)
	lacks(t, out, " ago   ", "commit since", "steps")

	// A title wider than the head is cut. In the steps a word wider than a
	// note's room is broken across rows rather than lost.
	id := fixture.ID("4e4e4e4e")
	fixture.New().Title("A title far longer than the narrow head beside the steps").Prompt("start").Write(t, w.projects, "p", id)
	w.ok("note", id, "see https://github.com/acme/app/pull/7145#discussion_r1")
	w.env = map[string]string{"COLUMNS": "40"}
	contains(t, w.ok("show", id, "--head"), "A title far longer than the narrow head…\n4e4e4e4e\n", "notes   now   see https://github.com/ac…\n")
	contains(t, w.ok("show", id, "--no-head"),
		"  note   now   see\n"+
			"               https://github.com/acme/a\n"+
			"               pp/pull/7145#discussion_r\n"+
			"               1\n")
}

// The popup's side column can be as narrow as 44 columns, and the head fits
// it whole: no row runs past the width, a link or a warning wider than the
// width is folded rather than cut, the path and the branch take a line each
// when they do not fit one, a row gives its time as a cell does, and a note
// is one line, cut, since the steps beside it keep its every word. A glyph
// before an item takes its room, and what an item folds starts under its
// text.
func TestTheHeadFitsTheSideColumn(t *testing.T) {
	const url = "https://github.com/acme/an-application-with-a-long-name/pull/1234"
	w := newWorld(t)
	id := fixture.ID("5a5a5a5a")
	tr := fixture.New().Title("Fits the side")
	tr.Prompt("start")
	tr.Bash("envoy run spike-review-r1 --with codex --prompt-file /tmp/s.md", "Command running in background")
	tr.Bash(`sh -c "git commit -m x"`, "[main 1a2b3c4] x") // a commit the reader may have missed
	tr.Raw(fixture.Row{"type": "pr-link", "prNumber": 1234, "prUrl": url, "prRepository": "acme/an-application-with-a-long-name"})
	for _, row := range tr.Rows {
		if _, ok := row["cwd"]; ok {
			row["cwd"], row["gitBranch"] = "/work/a-checkout-with-a-long-name", "feat/a-branch-name"
		}
	}
	tr.Write(t, w.projects, "p", id)
	note := "the spike covered the verify pass, so skip it until the importer lands on the new schema"
	fixture.WriteFile(t, filepath.Join(w.state, "notes", id+".jsonl"),
		[]byte(fmt.Sprintf(`{"at":%q,"text":%q}`+"\n", w.now.Add(-2*time.Hour).Format(time.RFC3339), note)+"{not a note\n"))

	w.env = map[string]string{"COLUMNS": "44"}
	head := w.ok("show", id, "--head")
	for _, row := range strings.Split(head, "\n") {
		if screen.StringWidth(row) > 44 {
			t.Errorf("a row takes %d columns:\n%s", screen.StringWidth(row), row)
		}
	}
	contains(t, head,
		"/work/a-checkout-with-a-long-name\nfeat/a-branch-name\nlast message 3 hours ago\n",
		"PR #1234 linked\n"+url[:44]+"\n"+url[44:]+"\n",
		"the reader may have missed 1 × commit\n(claude-steps check)\n",
		"notes   2h   the spike covered the verify p…\n"+
			"             1 note line could not be read\n",
	)
	lacks(t, head, "spike-review-r1")
	w.env["CLICOLOR_FORCE"] = "1"
	painted := w.ok("show", id, "--head")
	for _, row := range strings.Split(escape.ReplaceAllString(painted, ""), "\n") {
		if screen.StringWidth(row) > 44 {
			t.Errorf("a painted row takes %d columns:\n%s", screen.StringWidth(row), row)
		}
	}
	contains(t, escape.ReplaceAllString(painted, ""), "\uf07c /work/a-checkout-with-a-long-name\n\ue0a0 feat/a-branch-name\n")
	// Narrower than a marked item, the item folds under its text, whole.
	w.env["COLUMNS"] = "30"
	narrow := escape.ReplaceAllString(w.ok("show", id, "--head"), "")
	for _, row := range strings.Split(narrow, "\n") {
		if screen.StringWidth(row) > 30 {
			t.Errorf("a painted row takes %d columns:\n%s", screen.StringWidth(row), row)
		}
	}
	contains(t, narrow, "\uf07c /work/a-checkout-with-a-long\n  -name\n")
	w.env["COLUMNS"] = "44"
	delete(w.env, "CLICOLOR_FORCE")

	// The steps stand on their own: what the view may lack comes first, and
	// the note keeps its every word, folded under its text.
	timeline := w.ok("show", id, "--no-head")
	if !strings.HasPrefix(timeline, "the reader may have missed 1 × commit\n(claude-steps check)\n") {
		t.Errorf("the steps do not open with what the view may lack:\n%s", timeline)
	}
	contains(t, timeline,
		"  note   2h   the spike covered the verify\n"+
			"              pass, so skip it until the\n"+
			"              importer lands on the new\n"+
			"              schema\n")
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
