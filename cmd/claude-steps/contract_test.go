package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The output other repositories read, byte for byte: the popup
// (tmux-steps.sh) parses board --ids --brief and shows show --head,
// show --no-head and show, painted to the width it gives; rex reads
// show --json and board --json; claude-tomini sends a notes file to
// import-notes. docs/design.md § Contracts other repositories depend on.
// A file under testdata/contract that changes changes what those callers
// read, so change them in step; STEPS_CONTRACT_UPDATE=1 rewrites the files.
func TestContracts(t *testing.T) {
	w := newWorld(t)
	// A note may read like an option after "--", as the popup sends it.
	w.ok("note", "%1", "--", "--a note that reads like an option")
	w.now = w.now.Add(time.Minute)
	w.ok("note", worked, "a second note")

	popup := map[string]string{"CLICOLOR_FORCE": "1", "COLUMNS": "40"}
	panel := map[string]string{"CLICOLOR_FORCE": "1", "COLUMNS": "90"}
	for _, c := range []struct {
		name string
		env  map[string]string
		args []string
	}{
		{"board", nil, []string{"board"}},
		{"board-ids", nil, []string{"board", "--ids"}},
		{"board-ids-brief", nil, []string{"board", "--ids", "--brief"}},
		{"board-ids-brief-popup", popup, []string{"board", "--ids", "--brief"}},
		{"board-brief", nil, []string{"board", "--brief"}},
		{"board-json", nil, []string{"board", "--json"}},
		{"show", nil, []string{"show", "%1"}},
		{"show-all", nil, []string{"show", "%1", "--all"}},
		{"show-stacked", panel, []string{"show", worked}},
		{"show-stacked-all", panel, []string{"show", "--all", worked}},
		{"show-head", nil, []string{"show", "--head", "%1"}},
		{"show-head-popup", popup, []string{"show", "--head", worked}},
		{"show-no-head-popup", panel, []string{"show", "--no-head", worked}},
		{"show-no-head-all-popup", panel, []string{"show", "--no-head", "--all", worked}},
		{"show-json", nil, []string{"show", worked, "--json"}},
		{"show-missing-no-head", nil, []string{"show", "--no-head", "%3"}},
		{"show-unreadable-head", nil, []string{"show", "--head", "%4"}},
		{"show-partial", nil, []string{"show", "%5"}},
	} {
		w.env = c.env
		// The JSON names each transcript's path, under the temporary home.
		pinFile(t, c.name, strings.ReplaceAll(w.ok(c.args...), w.home, "$HOME"))
	}
	w.env = nil

	notes := filepath.Join(w.state, "notes", worked+".jsonl")
	pinFile(t, "notes-file", read(t, notes))

	// import-notes adds what this machine lacks, says how many on stdout,
	// and fails on a line that is not a note after merging the rest.
	w.stdin = `{"at":"2026-10-01T09:00:00Z","text":"from the laptop"}` + "\n" +
		`{"at":"2026-10-01T12:30:00Z","text":"--a note that reads like an option"}` + "\n" + "not a note\n"
	out, _, code := w.run("import-notes", worked)
	if code == 0 {
		t.Error("import-notes with a line that is not a note exited 0")
	}
	pinFile(t, "import-notes", out)
	pinFile(t, "notes-file-imported", read(t, notes))
}

// show's JSON section names every key show --json prints, so the page an
// agent parses it from cannot fall behind the shape.
func TestTheJSONIsDescribed(t *testing.T) {
	w := newWorld(t)
	w.ok("note", "%1", "a note")
	var session struct {
		Top    map[string]json.RawMessage
		Labels []map[string]json.RawMessage `json:"labels"`
		Notes  []map[string]json.RawMessage `json:"notes"`
	}
	out := w.ok("show", "%1", "--json")
	if err := errors.Join(json.Unmarshal([]byte(out), &session.Top), json.Unmarshal([]byte(out), &session)); err != nil {
		t.Fatal(err)
	}
	_, section, _ := strings.Cut(w.ok("show", "--help"), "\nJSON:\n")
	section, _, _ = strings.Cut(section, "\n\n")
	for _, keys := range []map[string]json.RawMessage{session.Top, session.Labels[0], session.Notes[0]} {
		for key := range keys {
			if !regexp.MustCompile(`\b` + key + `\b`).MatchString(section) {
				t.Errorf("show --help's JSON section does not name %q:\n%s", key, section)
			}
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func pinFile(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("testdata", "contract", name+".txt")
	if os.Getenv("STEPS_CONTRACT_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: %v (STEPS_CONTRACT_UPDATE=1 writes it)", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s:\n--- got\n%s--- want\n%s", name, file, strings.ReplaceAll(got, "\x1b", `\e`), strings.ReplaceAll(string(want), "\x1b", `\e`))
	}
}
