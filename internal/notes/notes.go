// Package notes keeps the user's free-form notes, one append-only file per
// session id. A session's notes are readable whether or not its transcript
// still exists.
package notes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load returns a session's notes in file order and the number of lines that
// could not be read as a note. A session with no file has no notes.
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
	return notes, bad, nil
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
