// claude-steps shows what has happened in Claude Code sessions, read from
// their transcript files. It never writes to a session and calls no model.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/panes"
	"github.com/qiushiyan/claude-steps/internal/record"
	"github.com/qiushiyan/claude-steps/internal/render"
)

const usage = `claude-steps — what has happened in a Claude Code session, read from its transcript

  claude-steps show [<pane>|<session>] [--all] [--json]   one session: its labels, notes and steps
  claude-steps board [--json] [--ids]                     every Claude pane in tmux, one row each
  claude-steps note <pane>|<session> <text…>              append a note to a session
  claude-steps check                                      test the reader against recent transcripts
  claude-steps import-notes <session id>                  merge notes from another machine, read on stdin

<pane> is a tmux pane id such as %12; show defaults to the pane it runs in.
<session> is a session id, or its first eight or more characters.

show prints the newest first: each label's latest event and the commits made
since, the rounds dispatched here with no collect seen, your notes, and the
steps. A step is an event under a label, or a note; the commits between two
steps are one count line. A round is one step, at its dispatch: its name,
what the transcript holds of it when that is not a run and a collect that
returned a result, and the latest skill run before it. show --all prints the
whole timeline in the steps' place: skills run, snippets pasted, envoy rounds
and their collects, commits, pull requests, compactions, and your notes.

A view states what the transcript holds. It does not say a check is finished
or still covers the code; a prompt that only names a skill is shown as your
words. "no collect seen" means this transcript holds none: the round may be
running, collected from another session, or given up on. A dispatch replaced
under the same name is counted on the later one ("dispatched 2 times").

On the board a label's cell is the time of its latest event ("11m", "2d");
"+2" counts the commits made since that event started, "read" means a skill's
file was read and not loaded, "named" means a prompt named the skill and
nothing more was seen. "no collect" holds the time of the newest round with no
collect seen, and "×2" when there are two. "!" before a title says the
transcript was read with something missing; the session view says what.

A hue names a label and red marks an error or something unread; no colour
grades a date. Output is coloured on a terminal, or anywhere with
CLICOLOR_FORCE=1, and never with NO_COLOR set. COLUMNS is the width to fit.

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

type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	tty    bool // stdout is a terminal
	now    func() time.Time
	panes  func() ([]panes.Pane, error)
	getenv func(string) string

	// Set once the configuration is read, before a command runs.
	loader *record.Loader
	view   render.View
}

func main() {
	a := &app{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now, panes: panes.List, getenv: os.Getenv}
	if info, err := os.Stdout.Stat(); err == nil {
		a.tty = info.Mode()&os.ModeCharDevice != 0
	}
	os.Exit(a.run(os.Args[1:]))
}

// newView is how this invocation prints. A popup reads the output through a
// pipe, so it asks for colour and gives the width in the environment.
func (a *app) newView(cfg config.Config) render.View {
	home, _ := os.UserHomeDir()
	v := render.View{Now: a.now(), Home: home, Labels: cfg.Labels}
	if n, err := strconv.Atoi(a.getenv("COLUMNS")); err == nil && n > 0 {
		v.Width = n
	}
	force := a.getenv("CLICOLOR_FORCE")
	v.Color = a.getenv("NO_COLOR") == "" && (a.tty || force != "" && force != "0")
	return v
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

	var run func([]string) error
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
	// Help needs no configuration, so a broken file cannot hide it.
	if asksHelp(rest) {
		fmt.Fprint(a.stdout, usage)
		return 0
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(a.stderr, "claude-steps: %v\n", err)
		return 1
	}
	a.loader, a.view = record.NewLoader(cfg), a.newView(cfg)
	if err := run(rest); err != nil {
		fmt.Fprintf(a.stderr, "claude-steps: %v\n", err)
		return 1
	}
	return 0
}

// asksHelp reports whether -h or --help comes before any "--". After it, a
// note may read like an option.
func asksHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "--help":
			return true
		}
	}
	return false
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
func (a *app) target(token string) (string, *panes.Pane, error) {
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
	id, err := a.loader.Resolve(token)
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

func (a *app) show(args []string) error {
	set, rest, err := flags(args, "--json", "--all")
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
	id, pane, err := a.target(token)
	if err != nil {
		return err
	}
	session := render.Session{Pane: pane, Record: a.loader.Load(id)}
	if set["--json"] {
		return a.view.ShowJSON(a.stdout, session)
	}
	a.view.Show(a.stdout, session, set["--all"])
	return nil
}

func (a *app) board(args []string) error {
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
		wg.Go(func() { sessions[i] = render.Session{Pane: &live[i], Record: a.loader.Load(live[i].SessionID)} })
	}
	wg.Wait()
	if set["--json"] {
		return a.view.BoardJSON(a.stdout, sessions)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(a.stderr, "claude-steps: no tmux pane runs a Claude session")
		return nil
	}
	a.view.Board(a.stdout, sessions, set["--ids"])
	return nil
}

func (a *app) note(args []string) error {
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
	id, _, err := a.target(rest[0])
	if err != nil {
		return err
	}
	if err := a.loader.AddNote(id, a.now(), text); err != nil {
		return fmt.Errorf("the note was not saved: %w", err)
	}
	return nil
}

// importNotes merges notes another machine kept for a session. The
// transcript need not be here yet: claude-tomini copies both.
func (a *app) importNotes(args []string) error {
	_, rest, err := flags(args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || !record.IsSessionID(rest[0]) {
		return errors.New("import-notes takes one full session id, and the notes on stdin")
	}
	added, bad, err := a.loader.ImportNotes(rest[0], a.stdin)
	if err != nil {
		return fmt.Errorf("the notes were not merged: %w", err)
	}
	fmt.Fprintf(a.stdout, "%d notes added\n", added)
	if bad > 0 {
		return fmt.Errorf("%d lines on stdin were not notes and were skipped", bad)
	}
	return nil
}
