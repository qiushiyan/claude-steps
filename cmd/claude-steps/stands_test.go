package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Obligation 5: the tool's own words carry no verdict, and the user's words
// are shown as written.
func TestNoVerdictInTheToolsOwnWords(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{{"board"}, {"show", "%1"}, {"show", "%1", "--all"}, {"show", "%3"}, {"show", "%4"}, {"show", "%5"}} {
		out := strings.ToLower(w.ok(args...))
		lacks(t, out, "✓", "✔", "done", "passed", "stale", "fresh", "complete", "succeeded")
	}
	// His prompt says "done" and stays as he wrote it.
	contains(t, w.ok("show", "%2"), `you: "have we run pl-loopy-verify yet? I think it is done"`)
	w.ok("note", "%1", "review is done, docs passed")
	contains(t, w.ok("show", "%1"), "review is done, docs passed")
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(root, path)
		files[rel] = fmt.Sprintf("%x %s", sha256.Sum256(data), info.ModTime().Format(time.RFC3339Nano))
		return nil
	})
	return files
}

// Obligation 14: nothing under the home changes except the notes.
func TestOnlyTheNotesAreWritten(t *testing.T) {
	w := newWorld(t)
	before := snapshot(t, w.home)
	w.tmuxPane = "%1"
	for _, args := range [][]string{
		{"board"}, {"board", "--ids"}, {"board", "--json"}, {"show"}, {"show", "%2"}, {"show", "%3"}, {"show", "%4", "--json"},
		{"show", "%5"}, {"show", "aaaaaaaa"}, {"check"}, {"note", "%1", "one"}, {"note", worked, "two"}, {"note", "%3", "three"}, {"--help"},
	} {
		w.run(args...)
	}
	after := snapshot(t, w.home)
	notes := filepath.Join(".local", "state", "claude-steps", "notes")
	for path, sum := range after {
		if was, existed := before[path]; existed && was != sum {
			t.Errorf("%s was modified", path)
		} else if !existed && filepath.Dir(path) != notes {
			t.Errorf("%s was created outside the notes directory", path)
		}
	}
	for path := range before {
		if _, kept := after[path]; !kept {
			t.Errorf("%s was removed", path)
		}
	}
	if len(after) != len(before)+2 {
		t.Errorf("want two notes files, got %d new files", len(after)-len(before))
	}
}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	return strings.Fields(string(out))
}

// Obligation 15: the binary cannot reach a network, so it cannot call a model.
func TestNoNetworkInTheBinary(t *testing.T) {
	for _, pkg := range goList(t, "-deps", ".") {
		if pkg == "net" || strings.HasPrefix(pkg, "net/") || strings.HasPrefix(pkg, "crypto/tls") {
			t.Errorf("the binary links %s", pkg)
		}
	}
}

// The tool has no handle on a session. Its only child process is tmux
// list-panes, which reads; and one package knows where transcripts live
// (obligation 13, second half).
func TestOneReaderAndOneReadOnlyProcess(t *testing.T) {
	root := filepath.Join("..", "..")
	var execUsers, transcriptReaders []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, imp := range file.Imports {
			switch strings.Trim(imp.Path.Value, `"`) {
			case "os/exec", "syscall", "plugin":
				execUsers = append(execUsers, rel)
			}
		}
		if bytes.Contains(src, []byte("ProjectsDir")) {
			transcriptReaders = append(transcriptReaders, rel)
		}
		return nil
	})
	if got := strings.Join(execUsers, " "); got != filepath.Join("internal", "panes", "panes.go") {
		t.Errorf("packages that can start a process: %s", got)
	}
	if got := strings.Join(transcriptReaders, " "); got != filepath.Join("internal", "config", "config.go")+" "+filepath.Join("internal", "record", "load.go") {
		t.Errorf("files that know where transcripts live: %s", got)
	}

	src, _ := os.ReadFile(filepath.Join(root, "internal", "panes", "panes.go"))
	calls := regexp.MustCompile(`exec\.Command\(([^\n]*)\)\.`).FindAllStringSubmatch(string(src), -1)
	if len(calls) != 1 || calls[0][1] != `"tmux", "list-panes", "-a", "-F", format` {
		t.Errorf("tmux is called with: %q", calls)
	}
}
