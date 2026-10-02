package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const id = "11111111-1111-4111-8111-111111111111"

var at = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// Obligation 11: appends made at the same moment are all kept. Each append
// opens the file itself, as separate commands do; without append mode they
// would all write at offset zero and overwrite one another.
func TestConcurrentAppendsAreAllKept(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	const writers = 64
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			if err := store.Append(id, at.Add(time.Duration(i)*time.Second), fmt.Sprintf("note %d", i)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	notes, bad, err := store.Load(id)
	if err != nil || bad != 0 {
		t.Fatalf("load: %v, %d unreadable lines", err, bad)
	}
	seen := map[string]bool{}
	for _, n := range notes {
		seen[n.Text] = true
	}
	if len(seen) != writers {
		t.Errorf("kept %d of %d notes", len(seen), writers)
	}
}

func TestMalformedLineIsCountedAndTheRestShown(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	if err := store.Append(id, at, "first"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(store.Dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{not a note}\n{\"text\":\"no time\"}\n")
	f.Close()
	if err := store.Append(id, at.Add(time.Minute), "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	notes, bad, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 2 || len(notes) != 2 || notes[0].Text != "first" || notes[1].Text != "line one\nline two" {
		t.Errorf("got %d unreadable lines and notes %+v", bad, notes)
	}
}

// A write the system cut short leaves half a line. The next note must not be
// glued onto it.
func TestAppendAfterATornWrite(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	if err := store.Append(id, at, "whole"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(store.Dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"at":"2026-10-01T09:01:00Z","text":"cut sh`)
	f.Close()
	if err := store.Append(id, at.Add(2*time.Minute), "after the damage"); err != nil {
		t.Fatal(err)
	}
	notes, bad, _ := store.Load(id)
	if bad != 1 || len(notes) != 2 || notes[1].Text != "after the damage" {
		t.Errorf("got %d unreadable lines and notes %+v", bad, notes)
	}
}

func TestNoFileIsNoNotes(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "never-made")}
	notes, bad, err := store.Load(id)
	if err != nil || bad != 0 || len(notes) != 0 {
		t.Errorf("got %v, %d, %v", notes, bad, err)
	}
	if ids := store.IDs(); len(ids) != 0 {
		t.Errorf("ids: %v", ids)
	}
}

func TestUnreadableFileIsAnError(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(store.Dir, id+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(id); err == nil {
		t.Error("a notes file that cannot be read was reported as no notes")
	}
}
