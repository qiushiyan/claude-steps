// claude-steps shows what has happened in Claude Code sessions, read from
// their transcript files. It never writes to a session and calls no model.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/panes"
	"github.com/qiushiyan/claude-steps/internal/record"
	"github.com/qiushiyan/claude-steps/internal/render"
)

const usage = `claude-steps — what has happened in a Claude Code session, read from its transcript

  claude-steps show [<pane>|<session>] [--json]   one session: its timeline and notes
  claude-steps board [--json] [--ids]             every Claude pane in tmux, one row each
  claude-steps note <pane>|<session> <text…>      append a note to a session
  claude-steps check                              test the reader against recent transcripts
  claude-steps import-notes <session id>          merge notes from another machine, read on stdin

<pane> is a tmux pane id such as %12; show defaults to the pane it runs in.
<session> is a session id, or its first eight or more characters.

The timeline lists dated events: skills run, snippets pasted, envoy rounds and
their collects, commits, pull requests, compactions, and your notes. It states
what the transcript holds. It does not say a check is finished or still covers
the code; a prompt that only names a skill is shown as your words.

A label's cell is the time of its latest event; "+2" counts the commits made
since that event started, "read" means a skill's file was read and not loaded,
"named" means a prompt named the skill and nothing more was seen.

Notes belong to a session id. Resuming keeps the id, so the notes stay. /clear
starts a new id with an empty timeline and no notes. A forked session also has
a new id and no notes, and its timeline starts with the history it copied.

Not read: subagent transcripts, so a commit made by a subagent is not listed;
commits made by git merge, rebase or cherry-pick, or through a script.

import-notes takes the notes file of the same session from another machine
and adds the notes this machine lacks; claude-tomini uses it.

board --ids starts every line with the pane id, a tab, the session id and a
tab, for a picker.
--json prints RFC 3339 times and no relative ones.
check counts each fact two ways over the last week's transcripts. It exits
non-zero when a transcript is unreadable or has lines that do not decode, or
when a second trace saw more than one fact in ten that the reader's own rule
missed.

Labels (the board's columns) and paths: ~/.config/claude-steps/config.toml
Notes: $XDG_STATE_HOME/claude-steps/notes, default ~/.local/state
`

const (
	// checkWindow is how far back `check` reads.
	checkWindow = 7 * 24 * time.Hour
	// missTolerance: `check` fails a fact when more than one in this many of
	// its second traces has no match in the reader's own count.
	missTolerance = 10
)

type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
	panes  func() ([]panes.Pane, error)
	getenv func(string) string
}

func main() {
	a := &app{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now, panes: panes.List, getenv: os.Getenv}
	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Fprint(a.stdout, usage)
		return 0
	}

	var run func(session, []string) error
	switch cmd {
	case "show":
		run = a.show
	case "board":
		run = a.board
	case "note":
		run = a.note
	case "check":
		run = a.check
	case "import-notes":
		run = a.importNotes
	default:
		fmt.Fprintf(a.stderr, "claude-steps: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(a.stderr, "claude-steps: %v\n", err)
		return 1
	}
	home, _ := os.UserHomeDir()
	s := session{cfg: cfg, loader: record.NewLoader(cfg), view: render.View{Now: a.now(), Home: home}}
	if err := run(s, rest); err != nil {
		if errors.Is(err, errHelp) {
			fmt.Fprint(a.stdout, usage)
			return 0
		}
		fmt.Fprintf(a.stderr, "claude-steps: %v\n", err)
		return 1
	}
	return 0
}

// errHelp is returned by flags when the arguments ask for the usage.
var errHelp = errors.New("help")

// session is what one invocation works with.
type session struct {
	cfg    config.Config
	loader *record.Loader
	view   render.View
}

// load is the one way a command gets a session's record.
func (s session) load(id string, pane *panes.Pane) render.Session {
	rec := s.loader.Load(id)
	return render.Session{Pane: pane, Record: rec, Labels: record.Summarise(rec.Events, s.cfg.Labels)}
}

// flags splits arguments into the named switches that are present and the
// rest, and refuses a switch the command does not have. Everything after "--"
// is an argument, so a note may read like an option.
func flags(args []string, known ...string) (map[string]bool, []string, error) {
	set := map[string]bool{}
	var rest []string
	for i, arg := range args {
		if arg == "--" {
			return set, append(rest, args[i+1:]...), nil
		}
		if arg == "-h" || arg == "--help" {
			return nil, nil, errHelp
		}
		if !strings.HasPrefix(arg, "--") {
			rest = append(rest, arg)
			continue
		}
		ok := false
		for _, k := range known {
			ok = ok || arg == k
		}
		if !ok {
			return nil, nil, fmt.Errorf("unknown option %s", arg)
		}
		set[arg] = true
	}
	return set, rest, nil
}

// livePanes returns the Claude panes whose session id is well formed.
func (a *app) livePanes() ([]panes.Pane, error) {
	all, err := a.panes()
	if err != nil {
		return nil, err
	}
	var out []panes.Pane
	for _, p := range all {
		if record.IsSessionID(p.SessionID) {
			out = append(out, p)
		}
	}
	return out, nil
}

// target turns a pane id or a session id into a session, with its pane when
// it is live.
func (a *app) target(s session, token string) (string, *panes.Pane, error) {
	if strings.HasPrefix(token, "%") {
		live, err := a.livePanes()
		if err != nil {
			return "", nil, err
		}
		for _, p := range live {
			if p.ID == token {
				return p.SessionID, &p, nil
			}
		}
		return "", nil, fmt.Errorf("pane %s has no Claude session", token)
	}
	// The pane is only for display, so a missing tmux server is not an error.
	live, _ := a.livePanes()
	// A live session has no transcript before its first prompt, and the
	// popup addresses it by its full id.
	if record.IsSessionID(token) {
		for _, p := range live {
			if p.SessionID == token {
				return token, &p, nil
			}
		}
	}
	id, err := s.loader.Resolve(token)
	if err != nil {
		return "", nil, err
	}
	for _, p := range live {
		if p.SessionID == id {
			return id, &p, nil
		}
	}
	return id, nil, nil
}

func (a *app) show(s session, args []string) error {
	set, rest, err := flags(args, "--json")
	if err != nil {
		return err
	}
	var token string
	switch len(rest) {
	case 0:
		if token = a.getenv("TMUX_PANE"); token == "" {
			return errors.New("show needs a pane or a session id outside tmux")
		}
	case 1:
		token = rest[0]
	default:
		return errors.New("show takes one pane or session")
	}
	id, pane, err := a.target(s, token)
	if err != nil {
		return err
	}
	view := s.load(id, pane)
	if set["--json"] {
		return render.ShowJSON(a.stdout, view)
	}
	s.view.Show(a.stdout, view)
	return nil
}

func (a *app) board(s session, args []string) error {
	set, rest, err := flags(args, "--json", "--ids")
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return errors.New("board takes no arguments")
	}
	live, err := a.livePanes()
	if err != nil {
		return err
	}
	sessions := make([]render.Session, len(live))
	var wg sync.WaitGroup
	for i := range live {
		wg.Go(func() { sessions[i] = s.load(live[i].SessionID, &live[i]) })
	}
	wg.Wait()
	if set["--json"] {
		return render.BoardJSON(a.stdout, sessions)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(a.stderr, "claude-steps: no tmux pane runs a Claude session")
		return nil
	}
	s.view.Board(a.stdout, sessions, set["--ids"])
	return nil
}

func (a *app) note(s session, args []string) error {
	_, rest, err := flags(args)
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return errors.New("note needs a pane or session, then the text")
	}
	text := strings.TrimSpace(strings.Join(rest[1:], " "))
	if text == "" {
		return errors.New("the note is empty")
	}
	id, _, err := a.target(s, rest[0])
	if err != nil {
		return err
	}
	if err := s.loader.AddNote(id, a.now(), text); err != nil {
		return fmt.Errorf("the note was not saved: %w", err)
	}
	return nil
}

// importNotes merges notes another machine kept for a session. The
// transcript need not be here yet: claude-tomini copies both.
func (a *app) importNotes(s session, args []string) error {
	_, rest, err := flags(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || !record.IsSessionID(rest[0]) {
		return errors.New("import-notes takes one full session id, and the notes on stdin")
	}
	added, bad, err := s.loader.ImportNotes(rest[0], a.stdin)
	if err != nil {
		return fmt.Errorf("the notes were not merged: %w", err)
	}
	fmt.Fprintf(a.stdout, "%d notes added\n", added)
	if bad > 0 {
		return fmt.Errorf("%d lines on stdin were not notes and were skipped", bad)
	}
	return nil
}

// check reads every transcript changed in the last week through the same
// loader the views use, and reports each fact counted two ways.
func (a *app) check(s session, args []string) error {
	_, rest, err := flags(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return errors.New("check takes no arguments")
	}
	ids := s.loader.Recent(s.view.Now.Add(-checkWindow))
	records := make([]record.Record, len(ids))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Go(func() {
			gate <- struct{}{}
			rec := s.loader.Load(id)
			rec.Events, rec.Notes = nil, nil
			records[i] = rec
			<-gate
		})
	}
	wg.Wait()

	var partial, unreadLines, silent int
	var unreadable []string
	var facts []string
	totals := map[string]*record.Signal{}
	missedIn := map[string][]string{}
	for _, rec := range records {
		switch rec.Status {
		case record.Partial:
			partial++
			unreadLines += rec.UnreadLines
		case record.Unreadable:
			unreadable = append(unreadable, rec.ID)
			continue
		}
		if rec.Turns == 0 {
			silent++
		}
		for _, sig := range rec.Signals {
			t := totals[sig.Fact]
			if t == nil {
				t = &record.Signal{Fact: sig.Fact}
				totals[sig.Fact] = t
				facts = append(facts, sig.Fact)
			}
			t.Primary += sig.Primary
			t.Second += sig.Second
			t.Missed += sig.Missed
			if sig.Missed > 0 {
				missedIn[sig.Fact] = append(missedIn[sig.Fact], rec.ID[:8])
			}
		}
	}

	w := a.stdout
	fmt.Fprintf(w, "%d transcripts changed in the last 7 days\n", len(ids))
	if partial > 0 {
		fmt.Fprintf(w, "%d read in part, %d lines could not be decoded\n", partial, unreadLines)
	}
	if silent > 0 {
		fmt.Fprintf(w, "%d hold no conversation row\n", silent)
	}
	for _, id := range unreadable {
		fmt.Fprintf(w, "unreadable: %s\n", id)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-18s %8s %8s %8s   %s\n", "fact", "reader", "second", "missed", "reader's rule / second trace")
	var drift []string
	if len(unreadable) > 0 {
		drift = append(drift, "a transcript is unreadable")
	}
	// Claude Code writes whole lines, so a line it cannot have written in the
	// shape the reader knows means the shape moved.
	if partial > 0 {
		drift = append(drift, "lines do not decode")
	}
	// Every transcript without a conversation row means the row types moved.
	if len(ids) > 0 && silent+len(unreadable) == len(ids) {
		drift = append(drift, "no transcript holds a conversation row")
	}
	for _, fact := range facts {
		t := totals[fact]
		fmt.Fprintf(w, "%-18s %8d %8d %8d   %s\n", fact, t.Primary, t.Second, t.Missed, record.SignalNotes[fact])
		if t.Missed > 0 {
			fmt.Fprintf(w, "%-18s missed in %s\n", "", strings.Join(missedIn[fact], " "))
		}
		// A second trace is not a perfect superset: Claude Code itself now
		// and then leaves a created pull request without its link row. One
		// miss in ten is past what that explains.
		if t.Missed*missTolerance > t.Second {
			drift = append(drift, fmt.Sprintf("the reader missed %d of %d for %q", t.Missed, t.Second, fact))
		}
	}
	fmt.Fprintln(w)
	if len(drift) > 0 {
		fmt.Fprintln(w, "the transcript format may have changed: "+strings.Join(drift, "; "))
		return errors.New("check found drift")
	}
	fmt.Fprintln(w, "no drift: the second traces saw nothing the reader's rules keep missing")
	return nil
}
