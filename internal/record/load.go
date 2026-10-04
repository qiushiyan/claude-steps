package record

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/notes"
)

var (
	sessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	idPrefix  = regexp.MustCompile(`^[0-9a-f-]{8,36}$`)
)

// IsSessionID reports whether s has the shape of a full session id.
func IsSessionID(s string) bool { return sessionID.MatchString(s) }

// Loader turns a session id into a Record.
type Loader struct {
	projects string
	snippets config.Snippets
	mentions []mention
	notes    notes.Store
}

func NewLoader(cfg config.Config) *Loader {
	return &Loader{
		projects: cfg.ProjectsDir,
		snippets: cfg.Snippets,
		mentions: compileMentions(cfg.SkillNames()),
		notes:    notes.Store{Dir: cfg.NotesDir},
	}
}

// Load reads one session. It always returns a record: what could not be read
// is in Status, UnreadLines and NotesError, never dropped.
func (l *Loader) Load(id string) Record {
	rec := Record{ID: id, Status: Missing, Events: []Event{}, Notes: []notes.Note{}}
	if !IsSessionID(id) {
		return rec
	}
	if ns, bad, err := l.notes.Load(id); err != nil {
		rec.NotesError = err.Error()
	} else {
		rec.Notes = append(rec.Notes, ns...)
		rec.UnreadNotes = bad
	}

	path, err := l.find(id)
	if err != nil {
		// A directory that may hold the transcript could not be read, so the
		// session is not known to have none.
		rec.Status, rec.Path = Unreadable, l.projects
		if at := new(fs.PathError); errors.As(err, &at) {
			rec.Path = at.Path
		}
		return rec
	}
	if path == "" {
		return rec
	}
	rec.Path = path
	f, err := os.Open(path)
	if err != nil {
		rec.Status = Unreadable
		return rec
	}
	defer f.Close()

	d := newDecoder(&rec, l.snippets, l.mentions)
	err = d.read(f)
	d.finish()
	switch {
	case err != nil, d.recognised == 0 && rec.UnreadLines > 0:
		// Lines of which none decodes as a row mean the format moved; an
		// empty timeline built from them would read as "nothing ran".
		rec.Status = Unreadable
		rec.Events = []Event{}
	case rec.UnreadLines > 0:
		rec.Status = Partial
	default:
		rec.Status = OK
	}
	return rec
}

// find locates <id>.jsonl in any project directory, and returns "" when no
// directory holds it. A session keeps its file name when /cd moves it to
// another project. Copies Claude Code sets aside under longer names never
// match, because the name is compared whole. A directory that could not be
// looked in is an error when the transcript was found in no other: what
// cannot be read is not known to be missing.
func (l *Loader) find(id string) (string, error) {
	dirs, err := l.projectDirs()
	if err != nil {
		return "", err
	}
	var best string
	var newest time.Time
	var unread error
	for _, dir := range dirs {
		path := filepath.Join(dir, id+".jsonl")
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			unread = err
			continue
		case info.IsDir():
			continue
		}
		if best == "" || info.ModTime().After(newest) {
			best, newest = path, info.ModTime()
		}
	}
	if best == "" {
		return "", unread
	}
	return best, nil
}

// projectDirs lists the directories under the projects directory. A projects
// directory that does not exist holds none; one that cannot be listed is an
// error.
func (l *Loader) projectDirs() ([]string, error) {
	entries, err := os.ReadDir(l.projects)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		path := filepath.Join(l.projects, e.Name())
		// A file beside the project directories is not one of them.
		if e.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				continue
			}
		} else if !e.IsDir() {
			continue
		}
		dirs = append(dirs, path)
	}
	return dirs, nil
}

// transcripts calls fn for every transcript file under the projects
// directory. It returns the error of a directory it could not list, after
// reading the others.
func (l *Loader) transcripts(fn func(id string, modified time.Time)) error {
	dirs, err := l.projectDirs()
	if err != nil {
		return err
	}
	var unread error
	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			unread = err
			continue
		}
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), ".jsonl")
			if !ok || !IsSessionID(id) {
				continue
			}
			if info, err := f.Info(); err == nil && !info.IsDir() {
				fn(id, info.ModTime())
			}
		}
	}
	return unread
}

// Resolve turns what the user typed into a full session id: a full id, or a
// prefix of at least eight characters that matches exactly one session with a
// transcript or notes on this machine.
func (l *Loader) Resolve(token string) (string, error) {
	if !idPrefix.MatchString(token) {
		return "", fmt.Errorf("%q is not a session id: give a full id or at least its first eight characters", token)
	}
	found := map[string]bool{}
	unread := l.transcripts(func(id string, _ time.Time) {
		if strings.HasPrefix(id, token) {
			found[id] = true
		}
	})
	for _, id := range l.notes.IDs() {
		if IsSessionID(id) && strings.HasPrefix(id, token) {
			found[id] = true
		}
	}
	ids := slices.Sorted(maps.Keys(found))
	switch len(ids) {
	case 0:
		if unread != nil {
			return "", fmt.Errorf("cannot look for %s: %w", token, unread)
		}
		return "", fmt.Errorf("no session matches %s: no transcript and no notes on this machine", token)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%s matches %d sessions, give more of the id:\n  %s", token, len(ids), strings.Join(ids, "\n  "))
}

// Recent lists the sessions whose transcript changed at or after since. A
// directory that could not be listed is an error: the list would be short
// with nothing to say so.
func (l *Loader) Recent(since time.Time) ([]string, error) {
	seen := map[string]bool{}
	err := l.transcripts(func(id string, modified time.Time) {
		if !modified.Before(since) {
			seen[id] = true
		}
	})
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// ImportNotes merges notes for a session read from r, adding those the
// session lacks.
func (l *Loader) ImportNotes(id string, r io.Reader) (added, bad int, err error) {
	if !IsSessionID(id) {
		return 0, 0, fmt.Errorf("%q is not a session id", id)
	}
	return l.notes.Import(id, r)
}

// AddNote appends a note to a session.
func (l *Loader) AddNote(id string, at time.Time, text string) error {
	if !IsSessionID(id) {
		return fmt.Errorf("%q is not a session id", id)
	}
	return l.notes.Append(id, at, text)
}
