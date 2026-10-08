package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/fixture"
)

var labels = []config.Label{
	{Name: "review", Skills: []string{"review"}, Snippets: []string{"review-implementation", "app-review-verify"}, Jobs: []string{"review-"}, CountCommits: true},
	{Name: "verify", Skills: []string{"pl-loopy-verify"}, Snippets: []string{"app-review-verify"}, CountCommits: true},
	{Name: "prompts", Skills: []string{"prompt-engineering"}},
}

// app-review-verify opens with a slash command and asks for a second step,
// as a project's own snippet does. reviewVerify is its text after "/review ".
var snippets = config.Snippets{
	{Key: "review-implementation", Head: "review the implementation against the spec, obligation by obligation, and report"},
	{Key: "app-review-verify", Head: "/review full review. while you wait, run pl-loopy-verify on the local rig and co"},
}

const reviewVerify = "full review. While you wait, run pl-loopy-verify\non the local rig and compare against a baseline."

const session = "11111111-1111-4111-8111-111111111111"

// load writes the transcript under a temporary projects directory and reads
// it back through the loader, the way every command does.
func load(t *testing.T, tr *fixture.Transcript) Record {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{ProjectsDir: filepath.Join(dir, "projects"), NotesDir: filepath.Join(dir, "notes"), Labels: labels, Snippets: snippets}
	tr.Write(t, cfg.ProjectsDir, "-work-app", session)
	return NewLoader(cfg).Load(session)
}

// lines is the timeline as text, one event per line, without times.
func lines(rec Record) []string {
	var out []string
	for _, e := range rec.Events {
		line := string(e.Kind)
		for _, part := range []string{e.Via, e.Name, strings.Join(e.Names, "+"), e.Args, e.Text, e.Outcome, e.Repo, e.Trigger, e.Dir} {
			if part != "" {
				line += " | " + part
			}
		}
		if e.Kind == PR {
			line += fmt.Sprintf(" | #%d", e.Number)
		}
		if e.Command != "" {
			line += " | as /" + e.Command
		}
		if e.Dispatches > 0 {
			line += fmt.Sprintf(" | ×%d", e.Dispatches)
		}
		var flags []string
		for flag, on := range map[string]bool{"failed": e.Failed, "amend": e.Amend, "dispatched": e.Dispatched, "replaced": e.Redispatched,
			"collected": e.CollectedAt != nil, "collect-failed": e.CollectFailed, "opened-here": e.OpenedHere} {
			if on {
				flags = append(flags, flag)
			}
		}
		sort.Strings(flags)
		out = append(out, strings.Join(append([]string{line}, flags...), " | "))
	}
	return out
}

func want(t *testing.T, rec Record, expected ...string) {
	t.Helper()
	if rec.Status != OK {
		t.Fatalf("status %s, %d unread lines", rec.Status, rec.UnreadLines)
	}
	got := lines(rec)
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("events\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(expected, "\n  "))
	}
}

func signal(rec Record, fact string) Signal {
	for _, s := range rec.Signals {
		if s.Fact == fact {
			return s
		}
	}
	return Signal{}
}

// Obligation 1.
func TestSkillIsNamedByItsInvocation(t *testing.T) {
	tr := fixture.New()
	tr.Slash("review", "codex full review", "/home/u/.claude/skills/review")
	tr.Slash("deploy", "", "/home/u/plugins/cache/tools/1.2.0") // no skills/ segment in the path
	tr.Builtin("compact")
	tr.Builtin("login")
	// A command whose turn continues with a reminder is not a skill either.
	turn := tr.SlashOnly("context", "")
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:30:00.000Z", "promptId": turn, "isMeta": true,
		"message": fixture.Row{"content": "<system-reminder>context usage is at 40%</system-reminder>"}})
	prompt := tr.SlashOnly("model", "")
	tr.SkillCall("consult", "approach codex", prompt, false)
	want(t, load(t, tr),
		"skill | slash | review | codex full review",
		"skill | slash | deploy",
		"skill | tool | consult | approach codex",
	)
}

// A built-in command and a later Skill call share a prompt id when the call
// happens in the same turn. Only the row right after a command can be its
// expansion.
func TestBuiltinSharingAPromptIDWithALaterExpansionIsNotASkill(t *testing.T) {
	tr := fixture.New()
	prompt := tr.SlashOnly("compact", "")
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:05:00.000Z", "promptId": prompt,
		"message": fixture.Row{"content": "<local-command-stdout>Compacted</local-command-stdout>"}})
	tr.SkillCall("review", "", prompt, false)
	rec := load(t, tr)
	want(t, rec, "skill | tool | review")
	if s := signal(rec, "skill, typed"); s.Missed != 0 {
		t.Errorf("a tool call's expansion was counted as a typed skill the reader missed: %+v", s)
	}
}

// A built-in command that answers with a prompt of its own, such as /init,
// is followed by a meta row as a skill is, without the skill's opening line.
func TestPromptBuiltinIsNotASkill(t *testing.T) {
	tr := fixture.New()
	for _, name := range []string{"init", "review"} {
		prompt := tr.SlashOnly(name, "")
		tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:05:00.000Z", "promptId": prompt, "isMeta": true,
			"message": fixture.Row{"content": []fixture.Row{{"type": "text", "text": "Please analyze this codebase and create a CLAUDE.md file"}}}})
	}
	rec := load(t, tr)
	// "review" is a labelled name: typed and not loaded, it is the user's words.
	want(t, rec, "mention | review | /review")
}

// Obligation 2.
func TestFailedSkillCall(t *testing.T) {
	tr := fixture.New().Prompt("go")
	tr.SkillCall("review", "", "p", true)
	rec := load(t, tr)
	want(t, rec, "skill | tool | review | failed")
	if st := Summarise(rec.Events, labels)[0]; st.Latest != nil {
		t.Errorf("a skill that failed to load became the label's latest event: %+v", st.Latest)
	}
}

// Obligation 6.
func TestCommitIsACommandNotAWord(t *testing.T) {
	tr := fixture.New()
	tr.Bash("git log --grep=commit --oneline | head", "abc1234 fix")
	tr.Bash("git grep commit -- docs", "docs/a.md: commit early")
	tr.Bash(`echo "then run git commit -m x" && git status`, "then run git commit -m x")
	tr.BashError(`git add -A && git commit -m "rejected by the hook"`, "Exit code 1\npre-commit: lint failed")
	tr.Bash(`git commit --dry-run -m "not made"`, "On branch main")
	tr.BashPending(`git commit -m "no result yet"`)
	tr.Bash(`git commit -m "docs: say what git commit does"`, "[main 1a2b3c4] docs: say what git commit does")
	tr.Bash(`git -C /work/other commit -m "s"`, "")
	tr.Bash("cd /work/app && git commit -q -F - <<'EOF'\nfeat: from a here-document\n\nbody\nEOF", "")
	tr.Bash("git add f && git commit -q -m \"$(cat <<'EOF'\nfix: the \"quoted\" one; git commit && more\n\nbody\nEOF\n)\"", "")
	tr.Bash(`git commit --amend --no-edit`, "")
	tr.BashError(`git commit -m "made, then the test failed" && make test`, "[feat/thing 9f8e7d6] made, then the test failed\n 1 file changed\nExit code 2")
	tr.Bash("commit() { git commit -q -F -; }\ncommit <<'EOF'\nfix: one\nEOF\ncommit <<'EOF'\nfix: two\nEOF", "")
	tr.Bash(`git commit --message "long form"`, "")
	tr.Bash(`git commit --message="with equals"`, "")
	tr.Bash(`git commit -am "a cluster"`, "")
	tr.Bash(`git commit -F - <<< "a here-string"`, "")
	rec := load(t, tr)
	want(t, rec,
		"commit | docs: say what git commit does",
		"commit | s | /work/other",
		"commit | feat: from a here-document",
		`commit | fix: the "quoted" one; git commit && more`,
		"commit | amend",
		"commit | made, then the test failed",
		"commit | fix: one",
		"commit | fix: two",
		"commit | long form",
		"commit | with equals",
		"commit | a cluster",
		"commit | a here-string",
	)
	if s := signal(rec, "commit"); s.Missed != 0 {
		t.Errorf("commit signal: %+v", s)
	}
}

// A call that succeeded proves a commit only when the commit had to run. One
// that ran on a branch, after ||, in an if, case or loop, or in the
// background counts on git's own summary line alone.
func TestGuardedCommitNeedsGitsOwnLine(t *testing.T) {
	tr := fixture.New()
	tr.Bash("git diff --cached --quiet || git commit -qm skipped", "")
	tr.Bash(`if [ -n "$(git status --porcelain)" ]; then git commit -qam maybe; fi`, "")
	tr.Bash("for f in a b; do git commit -qm \"$f\" -- \"$f\"; done", "")
	tr.Bash("git commit -qm later &", "")
	tr.BashBackground(`git commit -qm "in a background call"`)
	tr.Bash("git diff --cached --quiet || git commit -m shown", "[main 1a2b3c4] shown\n 1 file changed")
	tr.Bash(`cd /work/app || exit 1; git commit -qm "after a guard"`, "")
	rec := load(t, tr)
	want(t, rec,
		"commit | shown",
		"commit | after a guard",
	)
}

// A commit made somewhere other than the session's directory says where.
// A cd inside a subshell does not move the commands after it (review r1).
func TestDirectoryOfACommit(t *testing.T) {
	tr := fixture.New()
	tr.Bash(`cd /work/other && git commit -m "a"`, "")
	tr.Bash(`(cd sub && make) ; git commit -m "b"`, "")
	tr.Bash(`cd sub; git commit -m "c"`, "")
	tr.Bash(`git -C ../lib commit -m "d"`, "")
	tr.Bash(`cd /work/app && git commit -m "e"`, "")
	tr.Bash(`x=$(cd /tmp && pwd); git commit -m "f"`, "")
	// zsh's precommand modifiers run the builtin itself; command cd does too
	// in bash, but not in zsh, the shell the Bash tool runs.
	tr.Bash(`noglob cd /work/g; git commit -m "g"`, "")
	tr.Bash(`builtin cd /work/h && git commit -m "h"`, "")
	want(t, load(t, tr),
		"commit | a | /work/other",
		"commit | b",
		"commit | c | /work/app/sub",
		"commit | d | /work/lib",
		"commit | e",
		"commit | f",
		"commit | g | /work/g",
		"commit | h | /work/h",
	)
}

// A command substitution runs wherever its word is expanded: in a test, a
// loop's word list, a redirection or a here-document; so does a process
// substitution.
func TestCommandsInsideExpansionsRun(t *testing.T) {
	tr := fixture.New()
	tr.Bash(`[[ $(envoy collect review-r1 --result-only) = x ]]`, "findings")
	tr.Bash(`diff <(envoy collect review-r2 --result-only) result.md`, "")
	tr.Bash(`for s in $(git commit -qm "in a loop header" && echo ok); do :; done`, "")
	tr.Bash(`echo x > "$(git commit -qm "in a redirection"; echo out)"`, "")
	tr.Bash("cat <<EOF\n$(git commit -qm \"in a here-document\")\nEOF", "")
	tr.Bash("cat <<'EOF'\n$(git commit -qm \"quoted, so literal\")\nEOF", "")
	want(t, load(t, tr),
		"round | review-r1 | collected",
		"round | review-r2 | collected",
		"commit | in a loop header",
		"commit | in a redirection",
		"commit | in a here-document",
	)
}

// Tool events are dated at the call, however late the result arrives.
func TestToolEventsAreDatedAtTheCall(t *testing.T) {
	tr := fixture.New()
	tr.Raw(fixture.Row{"type": "assistant", "timestamp": "2026-10-01T09:00:00.000Z", "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_use", "id": "toolu-c", "name": "Bash", "input": fixture.Row{"command": `git commit -m "slow hook"`}},
		{"type": "tool_use", "id": "toolu-r", "name": "Read", "input": fixture.Row{"file_path": "/s/skills/prompt-engineering/SKILL.md"}},
	}}})
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:30:00.000Z", "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_result", "tool_use_id": "toolu-c", "content": ""}, {"type": "tool_result", "tool_use_id": "toolu-r", "content": "text"}}}})
	rec := load(t, tr)
	for _, e := range rec.Events {
		if !e.At.Equal(fixture.Start) {
			t.Errorf("%s dated %s, want the call's time", e.Kind, e.At)
		}
	}
	if len(rec.Events) != 2 {
		t.Errorf("events: %v", lines(rec))
	}
}

// A subagent's tool calls in the main file are not the session's own.
func TestSidechainToolCallsAreNotEvents(t *testing.T) {
	tr := fixture.New()
	tr.Raw(fixture.Row{"type": "assistant", "timestamp": "2026-10-01T09:00:00.000Z", "isSidechain": true, "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_use", "id": "toolu-s", "name": "Bash", "input": fixture.Row{"command": `git commit -m "by a subagent"`}}}}})
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:01:00.000Z", "isSidechain": true, "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_result", "tool_use_id": "toolu-s", "content": ""}}}})
	tr.SkillCall("plugin:review", "", "p", false)
	rec := load(t, tr)
	want(t, rec, "skill | tool | plugin:review")
	if st := Summarise(rec.Events, labels)[0]; st.Latest == nil || st.Latest.Name != "plugin:review" {
		t.Errorf("a plugin-prefixed skill should match its bare name in a label: %+v", st.Latest)
	}
}

// A skill file read in answer to a request after a run is the label's latest
// event: both are dated activity, and the later one says what the session
// last did.
func TestLaterReadOutranksAnEarlierRun(t *testing.T) {
	tr := fixture.New()
	tr.Slash("review", "", "/home/u/.claude/skills/review")
	tr.Prompt("follow skills/review/SKILL.md once more")
	tr.Read("/home/u/.claude/skills/review/SKILL.md", false)
	rec := load(t, tr)
	if st := Summarise(rec.Events, labels)[0]; st.Latest == nil || st.Latest.Kind != Read {
		t.Errorf("latest: %+v", st.Latest)
	}
}

// A read is a step only as the answer to a request. The model reads a skill's
// file as often to look something up as to run the skill, and a lookup is no
// stage: it stays in the history and dates no label. Once a request is
// answered, the same file read again under its prompt is a lookup too.
func TestAReadIsAStepOnlyAsTheAnswerToARequest(t *testing.T) {
	const rulebook = "/home/u/.claude/skills/prompt-engineering/SKILL.md"
	tr := fixture.New()
	tr.Prompt("tidy the error messages")
	tr.Read(rulebook, false)
	tr.Bash(`git commit -qm "one"`, "")
	rec := load(t, tr)
	want(t, rec, "read | prompt-engineering", "commit | one")
	if steps := Steps(rec.Events, labels); len(steps) != 0 {
		t.Errorf("a lookup is a step: %+v", steps)
	}
	if prompts := Summarise(rec.Events, labels)[2]; prompts.Latest != nil {
		t.Errorf("a lookup dates its label: %+v", prompts.Latest)
	}

	tr.Prompt("now follow the prompt-engineering skill")
	tr.Read(rulebook, false)
	tr.Read(rulebook, false)
	rec = load(t, tr)
	var got []string
	for _, s := range Steps(rec.Events, labels) {
		ask := "no request"
		if s.Ask != nil {
			ask = string(s.Ask.Kind)
		}
		got = append(got, fmt.Sprintf("%d %s %s: %s", s.Index, s.Event.Kind, strings.Join(s.Labels, "+"), ask))
	}
	if expected := []string{"3 read prompts: mention"}; !reflect.DeepEqual(got, expected) {
		t.Errorf("steps\n got %q\nwant %q", got, expected)
	}
	if prompts := Summarise(rec.Events, labels)[2]; prompts.Latest == nil || !prompts.Latest.At.Equal(rec.Events[3].At) {
		t.Errorf("prompts: %+v", prompts.Latest)
	}
}

// A request is said on every run and round under its label in the prompt it
// was answered in. A command typed to answer the previous prompt's paste
// leaves the paste said there, not the command; a command typed with nothing
// to answer is its own prompt's request. Where no prompt is known, nothing is
// carried from one round to the next.
func TestARequestIsSaidOnEveryRoundItsPromptRan(t *testing.T) {
	rounds := func(tr *fixture.Transcript) []string {
		rec := load(t, tr)
		var got []string
		for _, s := range Steps(rec.Events, labels) {
			if s.Event.Kind != Round {
				continue
			}
			ask := "nothing"
			if s.Ask != nil {
				ask = string(s.Ask.Kind) + " " + s.Ask.Name
			}
			got = append(got, s.Event.Name+" says "+ask)
		}
		return got
	}
	dispatch := func(tr *fixture.Transcript, names ...string) {
		for _, n := range names {
			tr.Bash("envoy run "+n+" --with codex --prompt-file /tmp/p.md", "Command running in background")
		}
	}

	tr := fixture.New()
	tr.Prompt("<pasted_content id=\"1\">\n/review " + reviewVerify + "\n</pasted_content>")
	tr.Slash("review", "codex full", "/home/u/.claude/skills/review")
	dispatch(tr, "review-r1", "review-r2")
	tr.Slash("review", "goal", "/home/u/.claude/skills/review")
	dispatch(tr, "review-r3", "review-r4")
	tr.Prompt("thanks")
	dispatch(tr, "review-r5")
	if got, expected := rounds(tr), []string{"review-r1 says snippet app-review-verify", "review-r2 says snippet app-review-verify",
		"review-r3 says skill review", "review-r4 says skill review", "review-r5 says nothing"}; !reflect.DeepEqual(got, expected) {
		t.Errorf("got  %q\nwant %q", got, expected)
	}

	old := fixture.New()
	old.Unsourced("review the branch")
	typed := old.SlashOnly("review", "codex")
	old.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:01:30.000Z", "isMeta": true, "promptId": typed,
		"message": fixture.Row{"content": []fixture.Row{{"type": "text", "text": "Base directory for this skill: /home/u/.claude/skills/review"}}}})
	dispatch(old, "review-r1", "review-r2")
	if got, expected := rounds(old), []string{"review-r1 says skill review", "review-r2 says nothing"}; !reflect.DeepEqual(got, expected) {
		t.Errorf("unmarked: got  %q\nwant %q", got, expected)
	}
}

// A session a program started through the SDK marks every prompt's source and
// none's origin. None of its prompts is the user's, so none is a request, and
// a read its prompt asked for is no step: the session reads no stage as the
// user's. A transcript that marks nothing is still read as the user's.
func TestAProgramsPromptsAreNotTheUsers(t *testing.T) {
	tr := fixture.New()
	tr.Program("# Review: the calendar. Hold the prompts to skills/prompt-engineering/SKILL.md, then run pl-loopy-verify.")
	tr.Read("/home/u/.claude/skills/prompt-engineering/SKILL.md", false)
	tr.SkillCall("pl-loopy-verify", "spikes", "p1", false)
	rec := load(t, tr)
	want(t, rec, "read | prompt-engineering", "skill | tool | pl-loopy-verify | spikes")
	states := Summarise(rec.Events, labels)
	if prompts := states[2]; prompts.Latest != nil {
		t.Errorf("prompts: %+v", prompts.Latest)
	}
	if verify := states[1]; verify.Latest == nil || verify.Latest.Kind != Skill {
		t.Errorf("verify: %+v", verify.Latest)
	}
	if s := signal(rec, "human prompt"); s.Primary != 0 || s.Missed != 0 {
		t.Errorf("human prompt signal: %+v", s)
	}
}

// A skill's file printed whole by a Bash call is the model reading it, as a
// Read call is. Asked in prose to run a skill, the model as often runs `cat`
// on its file as it calls a tool, and the prompt was then the only trace.
func TestASkillFilePrintedByCatIsARead(t *testing.T) {
	tr := fixture.New()
	tr.Slash("review", "codex full, and run pl-loopy-verify while you wait", "/home/u/.claude/skills/review")
	tr.Bash("wc -c .agents/skills/pl-loopy-verify/SKILL.md && cat .agents/skills/pl-loopy-verify/SKILL.md", "9000 SKILL.md\n---\nname: pl-loopy-verify")
	tr.Bash(`git commit -qm "fix"`, "")
	rec := load(t, tr)
	want(t, rec,
		"mention | pl-loopy-verify | /review codex full, and run pl-loopy-verify while you wait | as /review",
		"skill | slash | review | codex full, and run pl-loopy-verify while you wait",
		"read | pl-loopy-verify",
		"commit | fix",
	)
	if verify := Summarise(rec.Events, labels)[1]; verify.Latest == nil || verify.Latest.Kind != Read || verify.CommitsSince == nil || *verify.CommitsSince != 1 {
		t.Errorf("verify: %+v", verify)
	}
	if review := Summarise(rec.Events, labels)[0]; review.Latest == nil || review.Latest.Kind != Skill {
		t.Errorf("review: %+v", review)
	}

	for src, want := range map[string]string{
		// Read from where the command runs, with cat's own options.
		"cd ~/.claude/skills/review && cat -n SKILL.md BRIEF-TEMPLATE.md": "review",
		// The file is looked for in two places: one read, whichever printed.
		"cat .claude/skills/review/SKILL.md 2>/dev/null || cat .agents/skills/review/SKILL.md": "review",
		"cat skills/review/SKILL.md; echo ====; cat skills/prompt-engineering/SKILL.md":        "review prompt-engineering",
		"S=review; cat .agents/skills/$S/SKILL.md":                                             "review",
		// Two copies of one skill printed by a call are one read.
		"cat ~/.claude/skills/review/SKILL.md; cat .agents/skills/review/SKILL.md": "review",
		// A passage looked up is not the file read.
		"grep -n verdict skills/review/SKILL.md; sed -n 40,60p skills/review/SKILL.md; head -20 skills/review/SKILL.md": "",
		"cat skills/review/SKILL.md | sed -n 15,80p": "",
		// What the call's output does not hold.
		"cat skills/review/SKILL.md > /tmp/copy.md; wc -l /tmp/copy.md": "",
		"body=$(cat skills/review/SKILL.md)":                            "",
		// A command that may not have run.
		"test -f skills/review/SKILL.md || cat ~/.claude/skills/review/SKILL.md": "",
		// A name the text does not give.
		`cat skills/*/SKILL.md; cat "skills/$1/SKILL.md"`: "",
		"cat skills/review/REFERENCE.md":                  "",
	} {
		var got []string
		for _, e := range load(t, fixture.New().Bash(src, "text")).Events {
			got = append(got, string(e.Kind)+" "+e.Name)
		}
		expected := []string{}
		for _, name := range strings.Fields(want) {
			expected = append(expected, "read "+name)
		}
		if strings.Join(got, ", ") != strings.Join(expected, ", ") {
			t.Errorf("%s\n   got %q\n  want %q", src, got, expected)
		}
	}

	// A call that returned an error, or one whose output never came back.
	unseen := fixture.New()
	unseen.BashError("cat skills/review/SKILL.md", "cat: skills/review/SKILL.md: No such file or directory")
	unseen.BashBackground("cat skills/review/SKILL.md")
	unseen.BashPending("cat skills/review/SKILL.md")
	want(t, load(t, unseen))
}

// Obligation 7.
func TestPullRequests(t *testing.T) {
	tr := fixture.New()
	tr.Bash(`gh pr create --base develop --title "feat: thing"`, "https://github.com/acme/app/pull/12")
	tr.PRLink("acme/app", 12)
	tr.PRLink("acme/app", 12)
	tr.Bash("git push", "To github.com:acme/tools")
	tr.PRLink("acme/tools", 7)
	tr.BashError("gh pr create --fill", "a pull request for branch \"x\" already exists:\nhttps://github.com/acme/site/pull/3")
	tr.PRLink("acme/site", 3)
	rec := load(t, tr)
	want(t, rec,
		"pr | acme/app | #12 | opened-here",
		"pr | acme/tools | #7",
		"pr | acme/site | #3",
	)
	if got := len(rec.PullRequests()); got != 3 {
		t.Errorf("pull requests: %d", got)
	}
}

// Claude Code now and then writes no link row for a pull request it saw
// created. The URL gh printed is the pull request, dated at the call; a link
// row that comes later is the same pull request.
func TestCreatedPullRequestWithNoLinkRow(t *testing.T) {
	tr := fixture.New()
	tr.Bash(`gh pr create --fill`, "https://github.com/acme/app/pull/12")
	tr.Bash(`gh pr create --fill`, "https://github.com/acme/app/pull/13")
	tr.PRLink("acme/app", 13)
	rec := load(t, tr)
	want(t, rec,
		"pr | acme/app | #12 | opened-here",
		"pr | acme/app | #13 | opened-here",
	)
	// The missing link row still counts for check, which watches the format,
	// but the view has nothing to warn about: the pull request is shown.
	if s := signal(rec, "pull request"); s.Missed != 1 || len(rec.Missed()) != 0 {
		t.Errorf("signal %+v, view caveats %+v", s, rec.Missed())
	}
}

// Obligation 8.
func TestCompaction(t *testing.T) {
	summary := "This session is being continued. Earlier the user ran pl-loopy-verify and /review."
	tr := fixture.New().Prompt("start")
	tr.Compaction("manual", false, summary)
	tr.Compaction("auto", true, summary)
	rec := load(t, tr)
	want(t, rec, "compaction | manual")
	if s := signal(rec, "compaction"); s.Primary != 1 || s.Missed != 0 {
		t.Errorf("compaction signal: %+v", s)
	}
}

// Obligation 9.
func TestSpacingMalformedAndLongLines(t *testing.T) {
	tr := fixture.New()
	tr.Slash("review", "codex", "/home/u/.claude/skills/review")
	tr.Bash(`git commit -m "fix: thing"`, strings.Repeat("x", 1_300_000))
	tr.PRLink("acme/app", 12)
	tr.Compaction("auto", false, "summary")
	compact := load(t, tr)
	if compact.Status != OK {
		t.Fatalf("compact form: %s", compact.Status)
	}

	var spaced bytes.Buffer
	for i, line := range bytes.Split(bytes.TrimSpace(tr.Bytes()), []byte{'\n'}) {
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
		spaced.WriteString(respace(v))
		spaced.WriteByte('\n')
		if i == 1 {
			spaced.WriteString(`{"type":"user","timestamp":` + "\n")
		}
	}
	dir := t.TempDir()
	cfg := config.Config{ProjectsDir: dir, NotesDir: filepath.Join(dir, "notes"), Labels: labels}
	fixture.WriteFile(t, filepath.Join(dir, "p", session+".jsonl"), spaced.Bytes())
	rec := NewLoader(cfg).Load(session)
	if rec.Status != Partial || rec.UnreadLines != 1 {
		t.Fatalf("status %s with %d unread lines, want partial with 1", rec.Status, rec.UnreadLines)
	}
	if !reflect.DeepEqual(lines(rec), lines(compact)) || len(rec.Events) != 4 {
		t.Errorf("spaced form read differently:\n got %q\nwant %q", lines(rec), lines(compact))
	}
}

// respace writes JSON with spaces after every separator, the way no version
// of Claude Code does and any could.
func respace(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			name, _ := json.Marshal(k)
			parts = append(parts, string(name)+" : "+respace(x[k]))
		}
		return "{ " + strings.Join(parts, " , ") + " }"
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, respace(e))
		}
		return "[ " + strings.Join(parts, " , ") + " ]"
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// Labels are not exclusive: one event can be under several, and the names
// come back in configuration order. A failed call is still under its label.
func TestAnEventCanBeUnderSeveralLabels(t *testing.T) {
	overlapping := []config.Label{
		{Name: "review", Skills: []string{"review"}, Jobs: []string{"review-"}},
		{Name: "docs", Skills: []string{"update-docs", "review"}, Snippets: []string{"handoff"}},
		{Name: "rounds", Jobs: []string{"re", "consult-"}},
	}
	for _, c := range []struct {
		e    Event
		want string
	}{
		{Event{Kind: Skill, Name: "plugin:review"}, "review docs"},
		{Event{Kind: Skill, Name: "review", Failed: true}, "review docs"},
		{Event{Kind: Read, Name: "update-docs"}, "docs"},
		{Event{Kind: Snippet, Name: "handoff"}, "docs"},
		// Typed as a slash command, a paste is that command's run under a
		// label that lists the command's skill.
		{Event{Kind: Snippet, Name: "handoff", Command: "plugin:review"}, ""},
		{Event{Kind: Snippet, Name: "handoff", Command: "consult"}, "docs"},
		{Event{Kind: Round, Name: "review-r1"}, "review rounds"},
		{Event{Kind: Round, Name: "spike-r1"}, ""},
		{Event{Kind: Mention, Names: []string{"update-docs"}}, "docs"},
		{Event{Kind: Mention, Names: []string{"review", "update-docs"}}, "review docs"},
		// Typed as a command's arguments, the words ask for nothing under a
		// label that lists the command's skill: the run is the request there.
		{Event{Kind: Mention, Names: []string{"update-docs"}, Command: "plugin:review"}, ""},
		{Event{Kind: Mention, Names: []string{"update-docs"}, Command: "consult"}, "docs"},
		{Event{Kind: Commit, Text: "review"}, ""},
	} {
		if got := strings.Join(LabelsOf(overlapping, c.e), " "); got != c.want {
			t.Errorf("%s %s %v: under %q, want %q", c.e.Kind, c.e.Name, c.e.Names, got, c.want)
		}
	}
}

// Obligation 18.
func TestMentions(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("run pl-loopy-verify with local spikes to re-prove the behaviour before we open the PR")
	tr.Prompt("please review the diff and tell me what you think")   // "review" is plain English
	tr.Prompt("did /review cover the worker?")                       // the slash form names the skill
	tr.Prompt("follow ~/.claude/skills/prompt-engineering/SKILL.md") // a hyphenated name counts bare
	tr.Slash("pl-loopy-verify", "local spikes", "/repo/.claude/skills/pl-loopy-verify")
	tr.Prompt("Review the implementation against the spec, obligation   by obligation, and report back. Use pl-loopy-verify after.")
	tr.SlashOnly("review", "codex") // typed, and no expansion was seen
	tr.Prompt("thanks")
	tr.Prompt("Pickup gate: ~/.claude/skills/review/pickup/design.md") // another file of the skill's is not the skill
	tr.Prompt("follow skills/review/SKILL.md")                         // its own file is
	// A command's arguments are words typed like any other: they ask for the
	// skills they name beside the command's own.
	tr.Slash("review", "codex full, then pl-loopy-verify", "/home/u/.claude/skills/review")
	tr.SlashOnly("review", "and prompt-engineering")
	tr.SlashOnly("model", "opus")
	tr.Prompt("thanks")
	tr.Prompt(`<pasted_content id="4463"> then run pl-loopy-verify </pasted_content> on it`) // the tag is Claude Code's
	want(t, load(t, tr),
		"mention | pl-loopy-verify | run pl-loopy-verify with local spikes to re-prove the behavi…",
		"mention | review | did /review cover the worker?",
		"mention | prompt-engineering | follow ~/.claude/skills/prompt-engineering/SKILL.md",
		"skill | slash | pl-loopy-verify | local spikes",
		"snippet | review-implementation",
		"mention | review | /review codex",
		"mention | review | follow skills/review/SKILL.md",
		"mention | pl-loopy-verify | /review codex full, then pl-loopy-verify | as /review",
		"skill | slash | review | codex full, then pl-loopy-verify",
		"mention | review+prompt-engineering | /review and prompt-engineering",
		"mention | pl-loopy-verify | then run pl-loopy-verify on it",
	)
}

// A snippet that opens with a slash command arrives wrapped, as a prompt, or
// bare, as that command. Either way it is a paste. As a command that loaded
// its skill it is the run too, and the paste is then a step only under a
// label the run is not under.
func TestAPasteArrivesAsAPromptOrAsItsCommand(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("<pasted_content id=\"4463\">\n/review " + reviewVerify + "\n</pasted_content>")
	tr.Slash("review", reviewVerify, "/home/u/.claude/skills/review")
	// No expansion: the command loaded nothing. What settles it is the next
	// prompt, a tool result, or the transcript's end.
	tr.SlashOnly("review", reviewVerify)
	tr.Prompt("thanks")
	tr.SlashOnly("review", reviewVerify)
	tr.Bash("ls", "")
	tr.SlashOnly("review", reviewVerify)
	rec := load(t, tr)
	ran := "skill | slash | review | full review. While you wait, run pl-loopy-verify on the local rig and compare against a baseline."
	want(t, rec,
		"snippet | app-review-verify",
		"snippet | app-review-verify | as /review",
		ran,
		"snippet | app-review-verify",
		"snippet | app-review-verify",
		"snippet | app-review-verify",
	)
	var under []string
	for _, e := range rec.Events[:4] {
		under = append(under, strings.Join(LabelsOf(labels, e), "+"))
	}
	if got := strings.Join(under, " "); got != "review+verify verify review review+verify" {
		t.Errorf("the labels each event is under: %s", got)
	}
}

// A paste is a label's dated event, with the commits made since, until a
// load follows it. A label whose skill the snippet names and that does not
// list the snippet shows nothing of the prompt.
func TestAPasteDatesItsLabelUntilALoadFollows(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("/review " + reviewVerify + " Then follow skills/prompt-engineering/SKILL.md.")
	tr.Bash(`git commit -q -m "one"`, "")
	rec := load(t, tr)
	want(t, rec, "snippet | app-review-verify", "commit | one")
	states := Summarise(rec.Events, labels)
	for _, st := range states[:2] {
		if st.Latest == nil || st.Latest.Kind != Snippet || st.CommitsSince == nil || *st.CommitsSince != 1 {
			t.Errorf("%s: %+v", st.Name, st)
		}
	}
	if prompts := states[2]; prompts.Latest != nil {
		t.Errorf("a prompt read as a paste was read for mentions too: %+v", prompts.Latest)
	}

	tr.SkillCall("pl-loopy-verify", "", "p", false)
	rec = load(t, tr)
	if verify := Summarise(rec.Events, labels)[1]; verify.Latest == nil || verify.Latest.Kind != Skill || *verify.CommitsSince != 0 {
		t.Errorf("verify after the skill loaded: %+v", verify)
	}

	// Asked again in a session that holds the skill, the paste is the later
	// event and the count runs from it.
	tr.Bash(`git commit -q -m "two"`, "")
	tr.Prompt("/review " + reviewVerify)
	tr.Bash(`git commit -q -m "three"`, "")
	rec = load(t, tr)
	if verify := Summarise(rec.Events, labels)[1]; verify.Latest == nil || verify.Latest.Kind != Snippet || *verify.CommitsSince != 1 {
		t.Errorf("verify after a second paste: %+v", verify)
	}
}

// Each event knows the human prompt it came under, counted from one: the
// prompt's own events, and what the session did until the next one. A task
// notification starts no prompt, and a slash command the user typed is one,
// whose words are read under it even when no expansion follows it.
func TestEventsKnowThePromptTheyCameUnder(t *testing.T) {
	tr := fixture.New()
	tr.SkillCall("review", "before any prompt", "p0", false)
	tr.Prompt("/review " + reviewVerify)
	tr.SkillCall("review", "codex", "p1", false)
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Notification("review-r1 finished")
	tr.SkillCall("pl-loopy-verify", "spikes", "p1", false)
	tr.Slash("review", "again", "/home/u/.claude/skills/review")
	tr.Read("/home/u/.claude/skills/pl-loopy-verify/SKILL.md", false)
	tr.Prompt("<command-message>pl-loopy-verify</command-message>\n<command-name>/pl-loopy-verify</command-name>\n<command-args>now</command-args>")
	tr.Prompt("next")
	var got []string
	for _, e := range load(t, tr).Events {
		got = append(got, fmt.Sprintf("%s %s %d", e.Kind, e.Name, e.Prompt))
	}
	expected := []string{"skill review 0", "snippet app-review-verify 1", "skill review 1", "round review-r1 1",
		"skill pl-loopy-verify 1", "skill review 2", "read pl-loopy-verify 2", "mention  3"}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("got  %q\nwant %q", got, expected)
	}
}

func TestMentionOnlyLabelHasNoCommitCount(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("have we run pl-loopy-verify? then use prompt-engineering")
	tr.Bash(`git commit -m "x"`, "")
	tr.Read("/home/u/.claude/skills/prompt-engineering/SKILL.md", false)
	tr.Read("/home/u/.claude/skills/review/SKILL.md", true)
	rec := load(t, tr)
	want(t, rec,
		"mention | pl-loopy-verify+prompt-engineering | have we run pl-loopy-verify? then use prompt-engineering",
		"commit | x",
		"read | prompt-engineering",
	)
	states := Summarise(rec.Events, labels)
	if verify := states[1]; verify.Latest == nil || verify.Latest.Kind != Mention || verify.CommitsSince != nil {
		t.Errorf("verify: %+v", verify)
	}
	if prompts := states[2]; prompts.Latest == nil || prompts.Latest.Kind != Read {
		t.Errorf("prompts: %+v", prompts)
	}
	if review := states[0]; review.Latest != nil {
		t.Errorf("a read that failed became the review label's event: %+v", review.Latest)
	}
}

// A compaction summary and a task notification carry no origin; neither is
// the user's prompt, whatever skills they name.
func TestOnlyHumanPromptsAreRead(t *testing.T) {
	tr := fixture.New().Prompt("start")
	tr.Unsourced("[Request interrupted by user]")
	tr.Unsourced("pl-loopy-verify finished in the background")
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T10:00:00.000Z", "origin": fixture.Row{"kind": "task-notification"}, "promptSource": "system",
		"message": fixture.Row{"content": "<task-notification>pl-loopy-verify</task-notification>"}})
	want(t, load(t, tr))

	// A transcript from before prompts carried an origin: plain rows are his.
	old := fixture.New()
	old.Unsourced("run pl-loopy-verify now")
	old.Unsourced("[Request interrupted by user]")
	old.Compaction("auto", false, "the user ran pl-loopy-verify")
	want(t, load(t, old),
		"mention | pl-loopy-verify | run pl-loopy-verify now",
		"compaction | auto",
	)
}

func TestEventsAreInTimeOrderWithFileOrderOnTies(t *testing.T) {
	late := fixture.Start.Add(2 * time.Hour)
	tr := fixture.New().At(late)
	tr.Bash(`git commit -m "second"`, "")
	tr.At(fixture.Start)
	tr.Bash(`git commit -m "first"`, "")
	tr.At(late.Add(time.Hour))
	tr.Bash(`git commit -m "third" && git commit --allow-empty -m "fourth"`, "")
	want(t, load(t, tr), "commit | first", "commit | second", "commit | third", "commit | fourth")
}

// Obligation 19, at the level of one record: each fact's second trace.
func TestSignalsCountWhatTheReaderMissed(t *testing.T) {
	consistent := fixture.New()
	consistent.Slash("review", "", "/home/u/.claude/skills/review")
	consistent.Bash("gh pr create --fill", "https://github.com/acme/app/pull/12")
	consistent.PRLink("acme/app", 12)
	consistent.Compaction("auto", false, "summary")
	for _, s := range load(t, consistent).Signals {
		if s.Missed != 0 {
			t.Errorf("consistent transcript: %+v", s)
		}
	}

	drifted := fixture.New()
	// A pull request created with no link row.
	drifted.Bash("gh pr create --fill", "https://github.com/acme/app/pull/12")
	// A typed skill whose command row lost its prompt id.
	drifted.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:10:00.000Z", "message": fixture.Row{"content": "<command-name>/review</command-name>"}})
	drifted.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:10:01.000Z", "isMeta": true, "promptId": "p-9",
		"message": fixture.Row{"content": "Base directory for this skill: /home/u/.claude/skills/review"}})
	// A typed prompt with no origin.
	drifted.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:11:00.000Z", "promptSource": "typed", "source": fixture.Row{"kind": "human"},
		"message": fixture.Row{"content": "hello"}})
	// A summary with no boundary row.
	drifted.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:12:00.000Z", "isCompactSummary": true, "message": fixture.Row{"content": "summary"}})
	// A model's skill expansion naming a tool call the reader never saw.
	drifted.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:13:00.000Z", "isMeta": true, "sourceToolUseID": "toolu-unseen",
		"message": fixture.Row{"content": "Base directory for this skill: /home/u/.claude/skills/review"}})
	// A commit and a collect the command parser cannot see.
	drifted.Bash(`bash -c 'git commit -m hidden'`, "[main 1a2b3c4] hidden")
	drifted.Bash(`sh -c "envoy collect review-r1"`, fmt.Sprintf(collected, "review-r1", "ok"))
	rec := load(t, drifted)
	for _, fact := range []string{"pull request", "skill, typed", "skill, model call", "human prompt", "compaction", "commit", "envoy round"} {
		if s := signal(rec, fact); s.Missed != 1 {
			t.Errorf("%s: missed %d, want 1 (%+v)", fact, s.Missed, s)
		}
	}
	// The pull request is shown from its URL, so the view warns of six.
	if len(rec.Missed()) != 6 {
		t.Errorf("Missed(): %+v", rec.Missed())
	}
}
