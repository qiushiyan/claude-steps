package record

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/fixture"
)

// A driver that cannot type a long prompt writes it to a file and types a
// pointer to it. None of these paths exists: the text the reader matches is
// the session's own read of the file, in the transcript.
const (
	pointed = "/tmp/driver/scratchpad/s1.md"
	notice  = "I am away; another session sends your next steps.\n\nThe step follows:\n\n"
	// The review-implementation snippet as the driver's file holds it.
	implementation = "Review the implementation against the spec, obligation by obligation,\nand report what each test pins.\n"
)

func pointerTo(path string) string { return "Read " + path + " and do what it asks." }

// The same snippet reaches a session three ways: pasted into the prompt, in
// a file a prompt points at, and in such a file sent while the session was
// busy. Each is the same paste under the same labels; the two in a file say
// so, and are dated at the pointer and under its prompt, though the read came
// after.
func TestAPasteInAPointedFileIsThePromptsPaste(t *testing.T) {
	typed := fixture.New()
	typed.Prompt(strings.TrimSuffix(implementation, "\n"))
	want(t, load(t, typed), "snippet | review-implementation")

	for _, queued := range []bool{false, true} {
		tr := fixture.New()
		tr.Prompt("start")
		if queued {
			tr.Queued(pointerTo(pointed))
		} else {
			tr.Prompt(pointerTo(pointed))
		}
		tr.ReadFile(pointed, notice+implementation)
		tr.SkillCall("review", "codex", "p2", false)
		rec := load(t, tr)
		want(t, rec, "snippet | file | review-implementation", "skill | tool | review | codex")
		paste := rec.Events[0]
		if !paste.At.Equal(fixture.Start.Add(time.Minute)) || paste.Prompt != 2 {
			t.Errorf("queued %t: the paste is at %s under prompt %d, want the pointer's", queued, paste.At, paste.Prompt)
		}
		if got := LabelsOf(labels, paste); len(got) != 1 || got[0] != "review" {
			t.Errorf("queued %t: labels %q", queued, got)
		}
		// The run that followed answers it, as it answers a paste typed.
		steps := Steps(rec.Events, labels)
		if len(steps) != 1 || steps[0].Ask == nil || steps[0].Ask.Via != "file" {
			t.Errorf("queued %t: steps %+v", queued, steps)
		}
		if s := signal(rec, "file pointer"); s.Second != 1 || s.Missed != 0 {
			t.Errorf("queued %t: %+v", queued, s)
		}
	}
}

// The file the pointer names is gone by the time most views are drawn; what
// counts is what the session read. A file that holds no snippet is a prompt
// that pasted nothing, read whole, and the pointer's own words, a sentence
// and a path, ask for nothing.
func TestAPointerIsReadFromTheSessionsReadOfItsFile(t *testing.T) {
	tr := fixture.New()
	tr.Prompt(pointerTo("/tmp/driver/scratchpad/pl-loopy-verify.md"))
	tr.ReadFile("/tmp/driver/scratchpad/pl-loopy-verify.md", notice+"Build the three ideas you would take.\n")
	rec := load(t, tr)
	want(t, rec)
	if s := signal(rec, "file pointer"); s.Primary != 1 || s.Missed != 0 {
		t.Errorf("a file read whole with no snippet: %+v", s)
	}
}

// A pointer waits for a read that gives its file whole, or a snippet in it.
// A failed read, a read of the file's start that holds only the notice, a
// read of another file and a Bash call whose output is more than the file
// each leave it waiting; a pointer nothing answered is one the reader may
// have missed a paste in.
func TestAPointerWaitsForItsFileWhole(t *testing.T) {
	for name, c := range map[string]struct {
		read  func(tr *fixture.Transcript)
		paste bool
	}{
		"read whole":          {func(tr *fixture.Transcript) { tr.ReadFile(pointed, notice+implementation) }, true},
		"printed by cat":      {func(tr *fixture.Transcript) { tr.Bash("cat "+pointed, notice+implementation) }, true},
		"cat from its folder": {func(tr *fixture.Transcript) { tr.Bash("cd /tmp/driver/scratchpad && cat s1.md", notice+implementation) }, false},
		"failed, then read":   {func(tr *fixture.Transcript) { tr.Read(pointed, true).ReadFile(pointed, notice+implementation) }, true},
		"its start, then read": {func(tr *fixture.Transcript) {
			tr.ReadReturns(tr.ReadCall(pointed), pointed, notice, 9).ReadFile(pointed, notice+implementation)
		}, true},
		"its start only":     {func(tr *fixture.Transcript) { tr.ReadReturns(tr.ReadCall(pointed), pointed, notice, 9) }, false},
		"another file":       {func(tr *fixture.Transcript) { tr.ReadFile("/tmp/driver/scratchpad/s2.md", notice+implementation) }, false},
		"numbered by cat":    {func(tr *fixture.Transcript) { tr.Bash("cat -n "+pointed, notice+implementation) }, false},
		"cat beside another": {func(tr *fixture.Transcript) { tr.Bash("cat /tmp/a.md "+pointed, notice+implementation) }, false},
		"cat into a pipe":    {func(tr *fixture.Transcript) { tr.Bash("cat "+pointed+" | head -50", notice+implementation) }, false},
		"cat and another":    {func(tr *fixture.Transcript) { tr.Bash("cat "+pointed+"; echo end", notice+implementation+"end") }, false},
		"cat that failed":    {func(tr *fixture.Transcript) { tr.BashError("cat "+pointed, notice+implementation) }, false},
		"cat set aside": {func(tr *fixture.Transcript) {
			tr.Bash("cat "+pointed, "<persisted-output>\nPreview (first 2KB):\n"+notice)
		}, false},
		"cat set aside, held": {func(tr *fixture.Transcript) {
			tr.Bash("cat "+pointed, "<persisted-output>\nPreview (first 2KB):\n"+notice+implementation)
		}, true},
		"never read": {func(tr *fixture.Transcript) {}, false},
	} {
		tr := fixture.New()
		tr.Prompt(pointerTo(pointed))
		c.read(tr)
		rec := load(t, tr)
		pasted := len(rec.Events) > 0 && rec.Events[0].Kind == Snippet
		if pasted != c.paste || len(rec.of(Snippet)) > 1 {
			t.Errorf("%s: events %q", name, lines(rec))
		}
		if s := signal(rec, "file pointer"); (s.Missed == 0) != c.paste || s.Second != 1 {
			t.Errorf("%s: %+v", name, s)
		}
	}
}

// Two pointers sent while the session was busy may both arrive before it
// reads either. Each read answers its own pointer, under that pointer's
// prompt; the same file sent twice is two prompts that carried one text.
func TestEachPointerKeepsItsOwnPrompt(t *testing.T) {
	second := "/tmp/driver/scratchpad/s2.md"
	tr := fixture.New()
	tr.Prompt("start")
	tr.Queued(pointerTo(pointed))
	tr.Queued(pointerTo(second))
	tr.ReadFile(pointed, notice+implementation)
	tr.ReadFile(second, "/review "+reviewVerify+"\n")
	tr.Prompt(pointerTo(second))
	tr.Prompt(pointerTo(second))
	tr.ReadFile(second, "/review "+reviewVerify+"\n")
	var got []string
	for _, e := range load(t, tr).Events {
		got = append(got, fmt.Sprintf("%s %s %d", e.Via, e.Name, e.Prompt))
	}
	if want := "file review-implementation 2,file app-review-verify 3,file app-review-verify 4,file app-review-verify 5"; strings.Join(got, ",") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
}

// The driver's paste and the user's own of the same snippet are two
// senders: sent before anything answered either, both stand.
func TestAPasteInAFileAndOneTypedAreTwoRequests(t *testing.T) {
	tr := fixture.New()
	tr.Prompt(pointerTo(pointed))
	tr.ReadFile(pointed, notice+implementation)
	tr.Prompt(strings.TrimSuffix(implementation, "\n"))
	tr.Prompt(pointerTo(pointed))
	tr.ReadFile(pointed, notice+implementation)
	var got []string
	for _, s := range Steps(load(t, tr).Events, labels) {
		got = append(got, s.Event.Via+" "+s.Event.Name)
	}
	if want := "file review-implementation, review-implementation,file review-implementation"; strings.Join(got, ",") != want {
		t.Errorf("steps %q", got)
	}
}

// A prompt the user typed while the session worked is taken mid-turn as an
// attachment, and is a prompt like any other: it counts, it pastes, and it
// names. What the harness queues itself is not the user's, and neither is a
// subagent's.
func TestAPromptTakenMidTurnIsAPrompt(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("start")
	tr.SkillCall("review", "first", "p1", false)
	tr.Queued("/review " + reviewVerify)
	tr.Queued("and then use prompt-engineering")
	tr.Attached(fixture.Row{"type": "queued_command", "prompt": "<task-notification>done</task-notification>",
		"commandMode": "task-notification", "origin": fixture.Row{"kind": "task-notification"}})
	tr.Raw(fixture.Row{"type": "attachment", "isSidechain": true, "timestamp": "2026-10-01T09:30:00.000Z",
		"attachment": fixture.Row{"type": "queued_command", "prompt": "run pl-loopy-verify", "origin": fixture.Row{"kind": "human"}, "humanTurn": true}})
	tr.Queued("")
	tr.Attached(fixture.Row{"type": "queued_command", "prompt": []fixture.Row{{"type": "text", "text": "have we run pl-loopy-verify?"}, {"type": "image"}},
		"commandMode": "prompt", "origin": fixture.Row{"kind": "human"}, "humanTurn": true})
	tr.Attached(fixture.Row{"type": "invoked_skills", "skills": []fixture.Row{}})
	rec := load(t, tr)
	var got []string
	for _, e := range rec.Events {
		got = append(got, fmt.Sprintf("%s %s%s %d", e.Kind, e.Name, strings.Join(e.Names, "+"), e.Prompt))
	}
	if want := "skill review 1,snippet app-review-verify 2,mention prompt-engineering 3,mention pl-loopy-verify 5"; strings.Join(got, ",") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
	if s := signal(rec, "human prompt"); s.Primary != 4 || s.Second != 5 || s.Missed != 0 {
		t.Errorf("human prompt: %+v", s)
	}

	// A prompt taken mid-turn that is marked the user's turn and names no
	// human origin is one the reader may be losing.
	drifted := fixture.New()
	drifted.Attached(fixture.Row{"type": "queued_command", "prompt": "hello", "origin": fixture.Row{"kind": "keyboard"}, "humanTurn": true})
	if s := signal(load(t, drifted), "human prompt"); s.Second != 1 || s.Missed != 1 {
		t.Errorf("an unknown origin: %+v", s)
	}
	// One whose prompt is not text or blocks is a line the reader cannot read.
	broken := fixture.New()
	broken.Attached(fixture.Row{"type": "queued_command", "prompt": 7, "origin": fixture.Row{"kind": "human"}})
	if rec := load(t, broken); rec.UnreadLines != 1 {
		t.Errorf("unread lines: %d", rec.UnreadLines)
	}
}

// A call's result can come after a prompt the session took mid-turn. What
// the result settles, a commit, a read or a collect, came under the prompt
// the call was made in.
func TestWhatACallSettlesComesUnderItsOwnPrompt(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("commit it")
	commit := tr.BashCall(`git commit -q -m "one"`)
	read := tr.ReadCall("/home/u/.claude/skills/review/SKILL.md")
	collect := tr.BashCall("envoy collect review-r1")
	tr.Queued("next")
	tr.BashReturns(commit, "", false)
	tr.ReadReturns(read, "/home/u/.claude/skills/review/SKILL.md", "# review", 0)
	tr.BashReturns(collect, fmt.Sprintf(collected, "review-r1", "ok"), false)
	var got []string
	for _, e := range load(t, tr).Events {
		got = append(got, fmt.Sprintf("%s %d", e.Kind, e.Prompt))
	}
	if want := "commit 1,read 1,round 1"; strings.Join(got, ",") != want {
		t.Errorf("got %s, want %s", strings.Join(got, ","), want)
	}
}

// The second trace for a pointer is any one-line prompt that opens by asking
// for a file to be read: a reworded pointer reads as missed, not as nothing.
func TestAPromptWordedLikeAPointerIsCounted(t *testing.T) {
	tr := fixture.New()
	tr.Prompt("Read /tmp/driver/scratchpad/s1.md and follow it.")
	tr.ReadFile(pointed, notice+implementation)
	tr.Prompt("Read the spec and tell me what is missing.")
	tr.Prompt("Read /tmp/a.md\nthen /tmp/b.md")
	rec := load(t, tr)
	want(t, rec)
	if s := signal(rec, "file pointer"); s.Second != 1 || s.Missed != 1 {
		t.Errorf("%+v", s)
	}
}
