package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
	{Name: "review", Skills: []string{"review"}, Snippets: []string{"review-implementation"}, Jobs: []string{"review-"}, CountCommits: true},
	{Name: "verify", Skills: []string{"pl-loopy-verify"}, CountCommits: true},
	{Name: "prompts", Skills: []string{"prompt-engineering"}},
}

var snippets = []config.Snippet{
	{Key: "review-implementation", Head: config.Squash("Review the implementation against the spec, obligation by obligation, and report")},
}

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
		for flag, on := range map[string]bool{"failed": e.Failed, "amend": e.Amend, "dispatched": e.Dispatched, "collected": e.CollectedAt != nil, "opened-here": e.OpenedHere} {
			if on {
				line += " | " + flag
			}
		}
		out = append(out, canonical(line))
	}
	return out
}

// canonical orders the flags of a line, which come from a map.
func canonical(line string) string {
	parts := strings.Split(line, " | ")
	flags := map[string]bool{"failed": true, "amend": true, "dispatched": true, "collected": true, "opened-here": true}
	var head, tail []string
	for _, p := range parts {
		if flags[p] {
			tail = append(tail, p)
		} else {
			head = append(head, p)
		}
	}
	sort.Strings(tail)
	return strings.Join(append(head, tail...), " | ")
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

const collected = "job: /home/u/.local/state/envoy/jobs/app-1/%s\nstatus: %s\nduration: 6m\n\n--- result.md ---\nfindings"

// A round this session only collected is dated at the collect call, like
// every tool event: a commit made while the collect ran comes after it
// (review r1).
func TestCollectOnlyRoundIsDatedAtTheCall(t *testing.T) {
	tr := fixture.New()
	tr.Raw(fixture.Row{"type": "assistant", "timestamp": "2026-10-01T09:00:00.000Z", "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_use", "id": "toolu-collect", "name": "Bash", "input": fixture.Row{"command": "envoy collect review-r4"}},
		{"type": "tool_use", "id": "toolu-commit", "name": "Bash", "input": fixture.Row{"command": `git commit -m "while it ran"`}},
	}}})
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:00:30.000Z", "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_result", "tool_use_id": "toolu-commit", "content": ""}}}})
	tr.Raw(fixture.Row{"type": "user", "timestamp": "2026-10-01T09:05:00.000Z", "message": fixture.Row{"content": []fixture.Row{
		{"type": "tool_result", "tool_use_id": "toolu-collect", "content": fmt.Sprintf(collected, "review-r4", "ok")}}}})
	rec := load(t, tr)
	review := Summarise(rec.Events, labels)[0]
	if review.Latest == nil || !review.Latest.At.Equal(fixture.Start) {
		t.Fatalf("the round should be dated at the collect call: %+v", review.Latest)
	}
	if review.CommitsSince != nil && *review.CommitsSince != 0 {
		t.Errorf("commits since: %d", *review.CommitsSince)
	}
}

// Obligation 3.
func TestRoundsAndTheCommitsSince(t *testing.T) {
	tr := fixture.New()
	tr.Slash("review", "codex", "/home/u/.claude/skills/review")
	tr.Bash("envoy run review-r1 --with codex:gpt --prompt-file /tmp/r1.md --timeout-min 60", "Command running in background with ID: b1")
	tr.Bash(`git add -A && git commit -q -m "fix: first finding"`, "")
	tr.Bash("envoy collect review-r1 2>&1", fmt.Sprintf(collected, "review-r1", "ok"))
	tr.Bash("envoy run review-r2 --with @review-r1 --prompt-file /tmp/r2.md", "Command running in background with ID: b2")
	tr.Bash(`git commit -q -m "fix: second finding"`, "")
	rec := load(t, tr)
	want(t, rec,
		"skill | slash | review | codex",
		"round | review-r1 | ok | collected | dispatched",
		"commit | fix: first finding",
		"round | review-r2 | dispatched",
		"commit | fix: second finding",
	)
	review := Summarise(rec.Events, labels)[0]
	if review.Latest == nil || review.Latest.Name != "review-r2" {
		t.Fatalf("the label's latest event should be the last round dispatched: %+v", review.Latest)
	}
	if review.CommitsSince == nil || *review.CommitsSince != 1 {
		t.Errorf("commits since the last dispatch: got %v, want 1", review.CommitsSince)
	}
}

// Obligation 4.
func TestCollectKeepsEnvoysWord(t *testing.T) {
	fan := "fan-out: /jobs/app-1/consult-r1\nstatus: partial — 1 of 2 turns returned a result\n\n=== member a ===\njob: /jobs/app-1/consult-r1/a\nstatus: ok\n"
	tr := fixture.New()
	tr.Bash("envoy run consult-r1 --with codex --with claude:opus --prompt-file b.md", "Command running in background")
	tr.Bash("envoy collect --status-only consult-r1", "job: /jobs/app-1/consult-r1\nstatus: running (4m)\n")
	tr.Bash("envoy collect consult-r1", fan)
	// The same name dispatched again is a new round with its own collect.
	tr.Bash("envoy run consult-r1 --with codex --prompt-file c.md", "Command running in background")
	tr.Bash("envoy collect '/jobs/app-1/consult-r1+2' > /tmp/out.txt; wc -l /tmp/out.txt", "     40 /tmp/out.txt")
	// A job another session dispatched.
	tr.Bash("envoy collect review-r9", fmt.Sprintf(collected, "review-r9", "no-result"))
	tr.BashError("envoy collect review-r3", "envoy: no job named review-r3")
	// Text that only mentions envoy.
	tr.Bash(`grep -rn "envoy run consult" docs | head; echo "envoy collect consult-r7"`, "docs/a.md:3: envoy run consult-r1")
	// Asking whether a job is still running delivers nothing.
	tr.Bash("JOB=verify-r1; envoy run $JOB --with codex --prompt-file v.md", "Command running in background")
	tr.Bash("envoy collect --status-only verify-r1", "job: /jobs/app-1/verify-r1\nstatus: running (2m)\n")
	rec := load(t, tr)
	want(t, rec,
		"round | consult-r1 | partial | collected | dispatched",
		"round | consult-r1 | collected | dispatched",
		"round | review-r9 | no-result | collected",
		"round | review-r3 | error | collected",
		"round | verify-r1 | dispatched",
	)
	if s := signal(rec, "envoy round"); s.Missed != 0 {
		t.Errorf("a status probe or a file read was counted as a round the reader missed: %+v", s)
	}
}

func TestCollectWithNoStatusKeepsTheStatusAlreadyRead(t *testing.T) {
	tr := fixture.New()
	tr.Bash("envoy run review-r1 --with codex --prompt-file b.md", "Command running in background")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "timeout"))
	tr.Bash("envoy collect --result-only review-r1 | head -50", "findings, with no status line")
	want(t, load(t, tr), "round | review-r1 | timeout | collected | dispatched")
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
	want(t, load(t, tr),
		"commit | a | /work/other",
		"commit | b",
		"commit | c | /work/app/sub",
		"commit | d | /work/lib",
		"commit | e",
		"commit | f",
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

// A run that returned an error is shown as such and is not the label's
// latest event; a collect given a fan-out member's directory joins its round.
func TestFailedRunAndMemberCollect(t *testing.T) {
	tr := fixture.New()
	tr.Bash("envoy run review-r1 --with codex --prompt-file a.md", "Command running in background")
	tr.BashError("envoy run review-r2 --with nobody --prompt-file a.md", "envoy: unknown voice nobody")
	tr.Bash("envoy collect '/home/u/.local/state/envoy/jobs/app-1/review-r1+2/codex'", "job: /jobs/review-r1+2/codex\nstatus: ok\n")
	rec := load(t, tr)
	want(t, rec, "round | review-r1 | ok | collected | dispatched", "round | review-r2 | dispatched | failed")
	if st := Summarise(rec.Events, labels)[0]; st.Latest == nil || st.Latest.Name != "review-r1" {
		t.Errorf("a failed run became the label's latest event: %+v", st.Latest)
	}
}

// A skill file read after a run is the label's latest event: both are dated
// activity, and the later one says what the session last did.
func TestLaterReadOutranksAnEarlierRun(t *testing.T) {
	tr := fixture.New()
	tr.Slash("review", "", "/home/u/.claude/skills/review")
	tr.Read("/home/u/.claude/skills/review/SKILL.md", false)
	rec := load(t, tr)
	if st := Summarise(rec.Events, labels)[0]; st.Latest == nil || st.Latest.Kind != Read {
		t.Errorf("latest: %+v", st.Latest)
	}
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

// Obligation 10.
func TestReadStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{ProjectsDir: dir, NotesDir: filepath.Join(dir, "notes")}
	loader := NewLoader(cfg)
	put := func(name string, data string) string {
		id := fixture.ID(name)
		fixture.WriteFile(t, filepath.Join(dir, "p", id+".jsonl"), []byte(data))
		return id
	}
	good := string(fixture.New().Prompt("hello").Bytes())

	cases := []struct {
		name string
		id   string
		want Status
	}{
		{"no row decodes", put("aaaaaaaa", "not json\nstill not json\n"), Unreadable},
		{"rows with no type", put("bbbbbbbb", `{"kind":"user","text":"x"}`+"\n"+`{"kind":"assistant"}`+"\n"), Unreadable},
		{"empty file", put("cccccccc", ""), OK},
		{"a session nobody has prompted yet", put("dddddddd", `{"type":"mode","mode":"default"}`+"\n"+`{"type":"cost-state"}`+"\n"), OK},
		{"a last line still being written", put("eeeeeeee", good+`{"type":"assistant","timest`), OK},
		{"a conversation row with no time", put("ffffffff", good+`{"type":"user","message":{"content":"x"}}`+"\n"), Partial},
		{"a field of another type", put("abababab", good+`{"type":"user","timestamp":"2026-10-01T09:00:00Z","origin":"human"}`+"\n"), Partial},
		// Review r1: a row is recognised only once the fields the reader uses
		// decode, a known tool's input of the wrong shape is an unread line,
		// and a half-written first line is a session still being written.
		{"only a row whose content has another type", put("cdcdcdcd", `{"type":"user","timestamp":"2026-10-01T09:00:00Z","message":{"content":5}}`+"\n"), Unreadable},
		{"a Bash input of another shape", put("efefefef", good+`{"type":"assistant","timestamp":"2026-10-01T09:01:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":5}}]}}`+"\n"), Partial},
		{"only a first line still being written", put("a1a1a1a1", `{"type":"user","timest`), OK},
		{"no file", fixture.ID("99999999"), Missing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := loader.Load(tc.id)
			if rec.Status != tc.want {
				t.Errorf("status %s, want %s", rec.Status, tc.want)
			}
			if rec.Status == Unreadable && len(rec.Events) != 0 {
				t.Errorf("an unreadable transcript has events")
			}
		})
	}

	// A copy Claude Code set aside under a longer name is not the transcript.
	aside := fixture.ID("12121212")
	fixture.WriteFile(t, filepath.Join(dir, "p", aside+".orphaned-1759000000.jsonl"), []byte(good))
	fixture.WriteFile(t, filepath.Join(dir, "p", aside+".jsonl.superseded-1759000000"), []byte(good))
	if rec := loader.Load(aside); rec.Status != Missing {
		t.Errorf("a set-aside copy was read as the transcript: %s", rec.Status)
	}
	if _, err := loader.Resolve("12121212"); err == nil {
		t.Errorf("a set-aside copy resolved as a session")
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
	want(t, load(t, tr),
		"mention | pl-loopy-verify | run pl-loopy-verify with local spikes to re-prove the behavi…",
		"mention | review | did /review cover the worker?",
		"mention | prompt-engineering | follow ~/.claude/skills/prompt-engineering/SKILL.md",
		"skill | slash | pl-loopy-verify | local spikes",
		"snippet | review-implementation",
		"mention | review | /review codex",
	)
}

func TestMentionOnlyLabelHasNoCommitCount(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("have we run pl-loopy-verify yet?")
	tr.Bash(`git commit -m "x"`, "")
	tr.Read("/home/u/.claude/skills/prompt-engineering/SKILL.md", false)
	tr.Read("/home/u/.claude/skills/review/SKILL.md", true)
	rec := load(t, tr)
	want(t, rec,
		"mention | pl-loopy-verify | have we run pl-loopy-verify yet?",
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

// Obligation 13, first half.
func TestResolve(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{ProjectsDir: filepath.Join(dir, "projects"), NotesDir: filepath.Join(dir, "notes")}
	loader := NewLoader(cfg)
	one, two, noted := "abcdef01-0000-4000-8000-000000000001", "abcdef01-0000-4000-8000-000000000002", "fedcba98-0000-4000-8000-000000000003"
	fixture.New().Prompt("a").Write(t, cfg.ProjectsDir, "p1", one)
	fixture.New().Prompt("b").Write(t, cfg.ProjectsDir, "p2", two)
	if err := loader.AddNote(noted, time.Now(), "kept after the transcript was deleted"); err != nil {
		t.Fatal(err)
	}
	if err := loader.AddNote(one, time.Now(), "a session with both a transcript and notes"); err != nil {
		t.Fatal(err)
	}

	if _, err := loader.Resolve("abcdef01"); err == nil || !strings.Contains(err.Error(), one) || !strings.Contains(err.Error(), two) {
		t.Errorf("an ambiguous prefix should be refused with both ids listed: %v", err)
	}
	if id, err := loader.Resolve("abcdef01-0000-4000-8000-000000000001"); err != nil || id != one {
		t.Errorf("full id: %q, %v", id, err)
	}
	if id, err := loader.Resolve("fedcba98"); err != nil || id != noted {
		t.Errorf("a session with only notes should resolve: %q, %v", id, err)
	}
	for _, bad := range []string{"abcdef0", "../../etc", "ABCDEF01", "99999999"} {
		if id, err := loader.Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) = %q, want an error", bad, id)
		}
	}
}

// /cd moves a transcript to another project directory under the same name.
// A stale copy left behind loses to the file written last.
func TestTranscriptIsFoundByIDInAnyProject(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{ProjectsDir: dir, NotesDir: filepath.Join(dir, "notes")}
	old := fixture.New().Prompt("a").Write(t, dir, "-old-project", session)
	fixture.New().Prompt("a").Bash(`git commit -m "after the move"`, "").Write(t, dir, "-new-project", session)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	rec := NewLoader(cfg).Load(session)
	if filepath.Base(filepath.Dir(rec.Path)) != "-new-project" || len(rec.Events) != 1 {
		t.Errorf("read %s with %d events", rec.Path, len(rec.Events))
	}
}
