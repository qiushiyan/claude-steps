package record

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qiushiyan/claude-steps/internal/fixture"
)

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
		"round | review-r3 | collect-failed | collected",
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

// waiting are the rounds still waiting for a collect: a dispatch under their
// name replaces them.
func waiting(rec Record) []Event {
	var out []Event
	for _, e := range rec.Events {
		if e.Waiting() {
			out = append(out, e)
		}
	}
	return out
}

// A round is waiting when this session dispatched it and the transcript holds
// no collect for it. A run that returned an error is not waiting.
func TestRoundsWithNoCollect(t *testing.T) {
	tr := fixture.New()
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash("envoy run review-r2 --with codex --prompt-file /tmp/r2.md", "Command running in background")
	tr.Bash("envoy collect review-r2", "job: /j/review-r2\nstatus: ok\n\n--- result.md ---\nfindings")
	tr.BashError("envoy run review-r3 --with codex --prompt-file /tmp/r3.md", "envoy: no such voice")
	tr.Bash("envoy collect review-r0", "job: /j/review-r0\nstatus: ok\n\n--- result.md ---\nfindings")
	tr.Bash("envoy run spike-r1 --with codex --prompt-file /tmp/s.md", "Command running in background")
	var names []string
	for _, e := range waiting(load(t, tr)) {
		names = append(names, e.Name)
	}
	if got := strings.Join(names, " "); got != "review-r1 spike-r1" {
		t.Errorf("uncollected: %q", got)
	}
}

// A name dispatched again before any collect is one round with two
// dispatches: the collect is of the later one, and the earlier is not waiting.
func TestANameDispatchedAgainIsNotWaiting(t *testing.T) {
	tr := fixture.New()
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "ok"))
	// A name used again after its collect is a new round, and the old one stands.
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	rec := load(t, tr)
	var got []string
	for _, e := range rec.Events {
		got = append(got, fmt.Sprintf("%t/%t", e.Redispatched, e.CollectedAt != nil))
	}
	if strings.Join(got, " ") != "true/false false/true false/false" {
		t.Errorf("redispatched/collected per round: %v", got)
	}
	if out := waiting(rec); len(out) != 1 || !out[0].At.After(*rec.Events[1].CollectedAt) {
		t.Errorf("uncollected: %+v", out)
	}
}

// A collect whose job the command text does not give is named by the job
// lines envoy printed: a loop's variable stands for each job it collected.
// With no job line the round cannot be shown, and the signal says so.
func TestCollectNamedByWhatEnvoyPrinted(t *testing.T) {
	tr := fixture.New()
	tr.Bash("envoy run review-r1 --with codex --prompt-file /tmp/r1.md", "Command running in background")
	tr.Bash("envoy run review-r2 --with codex --prompt-file /tmp/r2.md", "Command running in background")
	tr.Bash(`for j in review-r1 review-r2; do envoy collect "$j" 2>&1 | head -3; done`,
		"job: /j/app-1/review-r1\nstatus: ok\nduration: 6m\njob: /j/app-1/review-r2+2\nstatus: ok\nduration: 9m")
	tr.Bash(`envoy collect "$(sed -n 1p /tmp/coords)"`, fmt.Sprintf(collected, "consult-r1", "partial"))
	rec := load(t, tr)
	want(t, rec,
		"round | review-r1 | ok | collected | dispatched",
		"round | review-r2 | ok | collected | dispatched",
		"round | consult-r1 | partial | collected",
	)
	if s := signal(rec, "envoy round"); s.Missed != 0 {
		t.Errorf("signal: %+v", s)
	}

	hidden := fixture.New()
	hidden.Bash(`envoy collect $(sed -n 1p /tmp/coords) | sed -n '/result.md/,$p'`, "--- result.md ---\nfindings")
	hidden.Bash(`envoy run "$NAME" --with codex --prompt-file /tmp/r1.md`, "Command running in background")
	rec = load(t, hidden)
	want(t, rec)
	if s := signal(rec, "envoy round"); s.Missed != 2 {
		t.Errorf("signal: %+v", s)
	}
}

// A collect reads the round its name read when the command ran, and takes
// its own block of the output: two collects in one call each keep their own
// status, and a dispatch later in the call is a new round, not the one
// collected.
func TestACollectReadsTheRoundItsCommandNamed(t *testing.T) {
	const run = "envoy run %s --with codex --prompt-file /tmp/p.md"
	tr := fixture.New()
	tr.Bash(fmt.Sprintf(run, "review-r1"), "Command running in background")
	tr.Bash(fmt.Sprintf(run, "review-r2"), "Command running in background")
	tr.Bash("envoy collect review-r1; envoy collect review-r2",
		fmt.Sprintf(collected, "review-r1", "ok")+"\n"+fmt.Sprintf(collected, "review-r2", "partial"))
	want(t, load(t, tr),
		"round | review-r1 | ok | collected | dispatched",
		"round | review-r2 | partial | collected | dispatched",
	)

	tr = fixture.New()
	tr.Bash(fmt.Sprintf(run, "review-r1"), "Command running in background")
	tr.Bash("envoy collect review-r1; "+fmt.Sprintf(run, "review-r1"), fmt.Sprintf(collected, "review-r1", "ok"))
	rec := load(t, tr)
	want(t, rec,
		"round | review-r1 | ok | collected | dispatched",
		"round | review-r1 | dispatched",
	)
	if out := waiting(rec); len(out) != 1 || !out[0].At.After(rec.Events[0].At) {
		t.Errorf("the later dispatch is the one with no collect: %+v", out)
	}

	// A fan-out collected through a variable is one round under the set's
	// name, with the set's status. Its members are not rounds.
	tr = fixture.New()
	tr.Bash(`envoy collect "$(cat /tmp/job)"`, "fan-out: /jobs/app-1/review-r1\nstatus: partial — 1 of 2 turns returned a result\n\n"+
		"=== member codex ===\njob: /jobs/app-1/review-r1/codex\nstatus: ok\n\n=== member claude ===\njob: /jobs/app-1/review-r1/claude\nstatus: failed\n")
	want(t, load(t, tr), "round | review-r1 | partial | collected")
}

// What a run's call returned is read against what envoy printed. An error
// with no job line ran nothing: it replaces no round, and a collect by the
// name reads the round the name read before. An error after envoy's job line
// is a job that exists, so a collect joins it. A job envoy says ended ok did
// not fail, whatever a later command of the call returned.
func TestARunIsReadAgainstWhatEnvoyPrinted(t *testing.T) {
	const run = "envoy run review-r1 --with codex --prompt-file /tmp/p.md"
	started := "job: /jobs/app-1/review-r1\nprovider: codex\nnext: let this command run to completion\n"

	// Dispatched, a retry that ran nothing, a dispatch that replaces the
	// first, and a collect of that one.
	tr := fixture.New()
	tr.Bash(run, "Command running in background")
	tr.BashError(run, "envoy: unknown voice")
	tr.Bash(run, "Command running in background")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "ok"))
	rec := load(t, tr)
	want(t, rec,
		"round | review-r1 | dispatched | replaced",
		"round | review-r1 | dispatched | failed",
		"round | review-r1 | ok | ×2 | collected | dispatched",
	)

	// A run that ran nothing, then a collect: the job is not this session's.
	tr = fixture.New()
	tr.BashError(run, "envoy: unknown voice")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "partial"))
	rec = load(t, tr)
	want(t, rec, "round | review-r1 | dispatched | failed", "round | review-r1 | partial | collected")
	if st := Summarise(rec.Events, labels)[0]; st.Latest == nil || st.Latest.Outcome != "partial" {
		t.Errorf("the collect is the label's latest event: %+v", st.Latest)
	}

	// A run that created its job and returned an error, then its collect:
	// one round, which is the label's latest event because a result came back.
	tr = fixture.New()
	tr.BashError(run, started+"status: timeout — the turn hit its cap\n")
	tr.Bash("envoy collect review-r1", fmt.Sprintf(collected, "review-r1", "timeout"))
	rec = load(t, tr)
	want(t, rec, "round | review-r1 | timeout | collected | dispatched | failed")
	if st := Summarise(rec.Events, labels)[0]; st.Latest == nil || st.Latest.Name != "review-r1" {
		t.Errorf("a collected round is the label's latest event: %+v", st.Latest)
	}

	// A run whose output was cut to its last lines shows no block of its own.
	// The block the output does hold is the collect's: its status follows its
	// job line.
	tr = fixture.New()
	tr.Bash(run+" 2>&1 | tail -3; envoy collect review-r1 2>&1 | tail -22",
		"status: failed — the provider reported a failure\nresult: /jobs/app-1/review-r1/result.md\njob: /jobs/app-1/review-r1\nstatus: failed — the provider reported a failure\n")
	want(t, load(t, tr), "round | review-r1 | failed | collected | dispatched")

	// The job ended ok and a later command failed the call.
	tr = fixture.New()
	tr.BashError(run+"; false", started+"status: ok\n")
	want(t, load(t, tr), "round | review-r1 | dispatched")
}
