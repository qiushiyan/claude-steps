package record

import (
	"fmt"
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
	snippets []config.Snippet
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

	path, ok := l.find(id)
	if !ok {
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
	case err != nil, d.lines > 0 && d.recognised == 0:
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

// find locates <id>.jsonl in any project directory. A session keeps its file
// name when /cd moves it to another project. Copies Claude Code sets aside
// under longer names never match, because the name is compared whole.
func (l *Loader) find(id string) (string, bool) {
	dirs, err := os.ReadDir(l.projects)
	if err != nil {
		return "", false
	}
	var best string
	var newest time.Time
	for _, dir := range dirs {
		path := filepath.Join(l.projects, dir.Name(), id+".jsonl")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if best == "" || info.ModTime().After(newest) {
			best, newest = path, info.ModTime()
		}
	}
	return best, best != ""
}

// transcripts calls fn for every transcript file under the projects directory.
func (l *Loader) transcripts(fn func(id string, modified time.Time)) {
	dirs, err := os.ReadDir(l.projects)
	if err != nil {
		return
	}
	for _, dir := range dirs {
		files, err := os.ReadDir(filepath.Join(l.projects, dir.Name()))
		if err != nil {
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
}

// Resolve turns what the user typed into a full session id: a full id, or a
// prefix of at least eight characters that matches exactly one session with a
// transcript or notes on this machine.
func (l *Loader) Resolve(token string) (string, error) {
	if !idPrefix.MatchString(token) {
		return "", fmt.Errorf("%q is not a session id: give a full id or at least its first eight characters", token)
	}
	found := map[string]bool{}
	l.transcripts(func(id string, _ time.Time) {
		if strings.HasPrefix(id, token) {
			found[id] = true
		}
	})
	for _, id := range l.notes.IDs() {
		if IsSessionID(id) && strings.HasPrefix(id, token) {
			found[id] = true
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no session matches %s: no transcript and no notes on this machine", token)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%s matches %d sessions, give more of the id:\n  %s", token, len(ids), strings.Join(ids, "\n  "))
}

// Recent lists the sessions whose transcript changed at or after since.
func (l *Loader) Recent(since time.Time) []string {
	seen := map[string]bool{}
	l.transcripts(func(id string, modified time.Time) {
		if !modified.Before(since) {
			seen[id] = true
		}
	})
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// AddNote appends a note to a session.
func (l *Loader) AddNote(id string, at time.Time, text string) error {
	if !IsSessionID(id) {
		return fmt.Errorf("%q is not a session id", id)
	}
	return l.notes.Append(id, at, text)
}
