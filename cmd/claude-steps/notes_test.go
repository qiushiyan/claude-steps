package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/fixture"
	"github.com/qiushiyan/claude-steps/internal/panes"
)

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
