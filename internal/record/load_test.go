package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/fixture"
)

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

// A store that cannot be read is not a store with nothing in it: the record
// says unreadable and where, and a listing fails rather than come back
// short. A projects directory that does not exist holds no transcript.
func TestAStoreThatCannotBeReadIsNotEmpty(t *testing.T) {
	dir := t.TempDir()
	blocked := fixture.WriteFile(t, filepath.Join(dir, "projects"), []byte("not a directory"))
	l := NewLoader(config.Config{ProjectsDir: blocked, NotesDir: filepath.Join(dir, "notes")})
	if rec := l.Load(session); rec.Status != Unreadable || rec.Path != blocked {
		t.Errorf("a projects path that is a file: status %s, path %q", rec.Status, rec.Path)
	}
	if _, err := l.Recent(time.Time{}); err == nil {
		t.Error("the sessions of a store that cannot be listed were listed")
	}
	if _, err := l.Resolve("11111111"); err == nil || !strings.Contains(err.Error(), blocked) {
		t.Errorf("resolve should say what it could not read: %v", err)
	}

	// One project directory that cannot be listed, beside one that holds the
	// transcript: the transcript is read, and the listing still fails.
	projects := filepath.Join(dir, "store")
	fixture.New().Prompt("x").Write(t, projects, "open", session)
	closed := filepath.Join(projects, "closed")
	if err := os.Mkdir(closed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(closed, 0o755) })
	if _, err := os.ReadDir(closed); err == nil {
		t.Skip("this user can read a directory with no permissions")
	}
	l = NewLoader(config.Config{ProjectsDir: projects, NotesDir: filepath.Join(dir, "notes")})
	if rec := l.Load(session); rec.Status != OK {
		t.Errorf("a transcript beside an unreadable directory: status %s", rec.Status)
	}
	if rec := l.Load(fixture.ID("22222222")); rec.Status != Unreadable || rec.Path != filepath.Join(closed, fixture.ID("22222222")+".jsonl") {
		t.Errorf("a transcript that may be in the unreadable directory: status %s, path %q", rec.Status, rec.Path)
	}
	if _, err := l.Recent(time.Time{}); err == nil {
		t.Error("the listing left a directory out without saying so")
	}

	none := NewLoader(config.Config{ProjectsDir: filepath.Join(dir, "absent"), NotesDir: filepath.Join(dir, "notes")})
	if rec := none.Load(session); rec.Status != Missing {
		t.Errorf("no projects directory: status %s", rec.Status)
	}
	if ids, err := none.Recent(time.Time{}); err != nil || len(ids) != 0 {
		t.Errorf("no projects directory: %v, %v", ids, err)
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
