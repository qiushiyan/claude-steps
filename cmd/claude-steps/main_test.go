package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qiushiyan/clikit/clikittest"

	"github.com/qiushiyan/claude-steps/internal/fixture"
	"github.com/qiushiyan/claude-steps/internal/panes"
)

// Help needs no configuration: a broken file cannot hide the usage.
func TestHelpNeedsNoConfiguration(t *testing.T) {
	w := newWorld(t)
	fixture.WriteFile(t, filepath.Join(w.home, ".config", "claude-steps", "config.toml"), []byte("[[label]]\nskils = 1\n"))
	out, errb, code := w.run("show", "--help")
	if code != 0 || !strings.Contains(out, "claude-steps show") {
		t.Errorf("show --help with a broken configuration: exit %d, %q, %q", code, out, errb)
	}
	// Each command's page names the options the popup relies on.
	contains(t, out, "--head", "--no-head", "--all")
	contains(t, w.ok("board", "--help"), "--brief", "--ids")
	if _, errb, code := w.run("show", "%1"); code == 0 || !strings.Contains(errb, "skils") {
		t.Errorf("a broken configuration was not reported: exit %d, %q", code, errb)
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
	if out, errb, code := w.run("board", "--json"); code != 0 || out != "[]\n" || errb != "" {
		t.Errorf("board --json with no Claude pane: exit %d, %q, %q", code, out, errb)
	}
	if out, _, code := w.run("--help"); code != 0 || !strings.Contains(out, "/clear") || !strings.Contains(out, "forked session") {
		t.Errorf("--help should say what starts a new session id:\n%s", out)
	}
	if _, _, code := w.run("frobnicate"); code != 2 {
		t.Errorf("unknown command: exit %d", code)
	}
}

// Every help page, as an agent reads it. make golden rewrites them.
func TestHelpPages(t *testing.T) {
	newWorld(t) // a temporary home, so a page that ran a command would touch nothing real
	clikittest.Golden(t, app, "testdata/help")
}

// A mistake in the arguments exits 2 with the command's synopsis, and runs
// nothing: no note is written and nothing is printed on stdout.
func TestUsageErrors(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{
		{},
		{"frobnicate"},
		{"show"}, // outside tmux, with no session
		{"show", "%1", "%2"},
		{"show", "%1", "--head", "--no-head"},
		{"show", "%1", "--json", "--head"},
		{"show", "%1", "--json", "--no-head"},
		{"show", "%1", "--brief"},
		{"board", "%1"},
		{"board", "--json", "--brief"},
		{"note"},
		{"note", "%1"},
		{"note", "%1", "  "},
		{"note", "%1", "--to-session", "text"},
		{"check", "now"},
		{"import-notes"},
		{"import-notes", "aaaaaaaa"},
	} {
		out, errb, code := w.run(args...)
		if code != 2 || out != "" || !strings.Contains(errb, "Usage: claude-steps") {
			t.Errorf("claude-steps %q: exit %d, stdout %q, stderr %q", args, code, out, errb)
		}
	}
	if _, err := os.Stat(filepath.Join(w.state, "notes")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused command wrote notes: %v", err)
	}
}
