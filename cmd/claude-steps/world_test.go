package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
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

var escape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// glyph is a mark the head draws before an item when it paints.
var glyph = regexp.MustCompile("[\uf120\uf07c\ue0a0\uf066\uf017\uf407] ")

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
