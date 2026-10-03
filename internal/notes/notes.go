// Package notes keeps the user's free-form notes, one append-only file per
// session id. A session's notes are readable whether or not its transcript
// still exists.
//
// The file is only ever appended to, never rewritten, so a note written while
// another process adds notes is never lost. Reading sorts by time and drops
// exact repeats, which lets a merge from another machine append what it
// brings without coordinating with anyone.
package notes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Note struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Store is the directory holding one <session id>.jsonl file per session.
type Store struct {
	Dir string
}

func (s Store) path(id string) string {
	return filepath.Join(s.Dir, id+".jsonl")
}

// Append adds one note. The line reaches the file in a single write on a
// descriptor opened for appending, so two appends at once are both kept.
//
// A write the system cut short leaves a partial line. The next append starts
// on a line of its own, so the damage stays one unreadable line and never
// swallows a later note.
func (s Store) Append(id string, at time.Time, text string) error {
	line, err := json.Marshal(Note{At: at.UTC(), Text: text})
	if err != nil {
		return err
	}
	return s.appendBytes(id, append(line, '\n'))
}

// appendBytes writes whole lines to the end of a session's file in one call.
func (s Store) appendBytes(id string, lines []byte) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(id), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			lines = append([]byte{'\n'}, lines...)
		}
	}
	if _, err := f.Write(lines); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load returns a session's notes in time order, each once, and the number of
// lines that could not be read as a note. A session with no file has no notes.
func (s Store) Load(id string) (notes []Note, bad int, err error) {
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read the notes for %s: %w", id, err)
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var n Note
		if json.Unmarshal(line, &n) != nil || n.At.IsZero() {
			bad++
			continue
		}
		notes = append(notes, n)
	}
	slices.SortStableFunc(notes, func(a, b Note) int { return a.At.Compare(b.At) })
	notes = slices.CompactFunc(notes, func(a, b Note) bool { return a.At.Equal(b.At) && a.Text == b.Text })
	return notes, bad, nil
}

// Import adds the notes in r, one JSON note per line as this package writes
// them, that the session does not already hold. The new notes reach the file
// in one append, so notes written meanwhile are kept.
func (s Store) Import(id string, r io.Reader) (added, bad int, err error) {
	have, _, err := s.Load(id)
	if err != nil {
		return 0, 0, err
	}
	held := map[string]bool{}
	for _, n := range have {
		held[n.At.UTC().Format(time.RFC3339Nano)+"\x00"+n.Text] = true
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, err
	}
	var out []byte
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var n Note
		if json.Unmarshal(line, &n) != nil || n.At.IsZero() {
			bad++
			continue
		}
		key := n.At.UTC().Format(time.RFC3339Nano) + "\x00" + n.Text
		if held[key] {
			continue
		}
		held[key] = true
		encoded, err := json.Marshal(Note{At: n.At.UTC(), Text: n.Text})
		if err != nil {
			return 0, bad, err
		}
		out = append(append(out, encoded...), '\n')
		added++
	}
	if added == 0 {
		return 0, bad, nil
	}
	return added, bad, s.appendBytes(id, out)
}

// IDs lists the sessions that have a notes file.
func (s Store) IDs() []string {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok && !e.IsDir() {
			ids = append(ids, id)
		}
	}
	return ids
}
