package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/fixture"
)

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

// A store that cannot be read is said: the session view names what it could
// not read, and check fails rather than report no drift over nothing.
func TestAStoreThatCannotBeReadIsSaid(t *testing.T) {
	w := newWorld(t)
	blocked := fixture.WriteFile(t, filepath.Join(w.home, "projects-is-a-file"), []byte("not a directory"))
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), []byte(fmt.Sprintf("projects_dir = %q\n%s", blocked, configFile)))
	contains(t, w.ok("show", "%1"), "transcript unreadable  ~/projects-is-a-file\n")
	contains(t, w.ok("board"), "work:1.1", "transcript unreadable")
	out, errb, code := w.run("check")
	if code == 0 || strings.Contains(out, "no drift") || !strings.Contains(errb, "nothing was checked") {
		t.Errorf("check over a store it cannot list: exit %d, %q, %q", code, out, errb)
	}
}
