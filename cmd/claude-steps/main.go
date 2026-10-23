// claude-steps shows what has happened in Claude Code sessions, read from
// their transcript files. It never writes to a session and calls no model.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	"github.com/qiushiyan/clikit"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/panes"
	"github.com/qiushiyan/claude-steps/internal/record"
	"github.com/qiushiyan/claude-steps/internal/render"
)

type CLI struct {
	Show        ShowCmd        `cmd:"" help:"Print one session: its labels, its notes and its steps."`
	Board       BoardCmd       `cmd:"" help:"Print a row for each Claude session running in tmux."`
	Note        NoteCmd        `cmd:"" help:"Add a note to a session."`
	Check       CheckCmd       `cmd:"" help:"Test the reader's rules against the last week of transcripts."`
	ImportNotes ImportNotesCmd `cmd:"" help:"Merge the notes another machine kept for a session, read on stdin."`
}

func (CLI) Help() string {
	return `
A session's transcript records what ran in it: skills loaded, review rounds
handed to another model through envoy and collected, commits, pull requests
and compactions. claude-steps reads it from outside the session and dates
each fact under your labels, the stages such as review that the
configuration names, so you can tell which stages a session has been
through and what was committed since without asking it: a question costs
the session a turn and can set its model working.

Everything it prints is a dated fact, never a verdict. "review 2 hours ago,
1 commit since" is for you to judge; nothing says a check finished, passed
or still covers the code.`
}

func (CLI) HelpSections() []clikit.Section {
	return []clikit.Section{
		{Title: "Sessions", Body: sessions},
		{Title: "Files", Shared: true, Body: `
~/.config/claude-steps/config.toml
    Under $XDG_CONFIG_HOME when that is set. Each [[label]] table is a stage:
    its name; the skills, snippets (keys of TabType snippets, prompts kept
    in a file to paste whole) and jobs (envoy job-name prefixes) that count
    as it; count_commits, to count the commits
    made since its latest step; and color. The projects_dir key moves the
    transcripts, and the snippets key lists the TabType files. With no file
    there are no labels, and a session view prints its full history.
~/.claude/projects/
    The transcripts, opened read-only. A subagent's own transcript is not
    read.
~/.local/state/claude-steps/notes/<session id>.jsonl
    Your notes, one JSON note per line; under $XDG_STATE_HOME when that is
    set. They are the only files written: nothing reaches a session, no key
    is sent to its pane, and no model is called.`},
	}
}

// sessions is the Sessions section of the pages that name one.
const sessions = `
A session is one Claude Code conversation, named by its session id. Name
one by a tmux pane id such as %12 (the session that pane runs), by its full
id, or by at least its first eight characters; a live pane's session works
by its full id before it has a transcript. Outside tmux, a session's id is
its transcript's file name, ~/.claude/projects/<project>/<session id>.jsonl.
Resuming a session keeps its id, and so does moving it with /cd. /clear
starts a new id, with an empty timeline and no notes; a forked session also
gets a new id and no notes, and its timeline starts with the history it
copied.`

// colour is the Output section of the commands that print views.
const colour = `
Colour is drawn on a terminal, or through a pipe when CLICOLOR_FORCE is set
to anything but 0, and never when NO_COLOR is set; plain output carries no
escape codes. A hue names a label, red marks an error the transcript
reports or something the reader could not read, bold marks a count of
commits that is not zero, and no colour grades a date. COLUMNS, when set,
is the width the output fits; without it, long text is cut at fixed widths.`

type ShowCmd struct {
	Session string `arg:"" optional:"" env:"TMUX_PANE" help:"A pane id such as %12, a session id, or at least its first eight characters; the pane show runs in when omitted."`
	All     bool   `help:"Print the full history in place of the steps; with --head, list every note."`
	JSON    bool   `xor:"part" help:"Print the session as one JSON object (see JSON)."`
	Head    bool   `xor:"part" help:"Print only the head: the session, its labels and its notes."`
	NoHead  bool   `xor:"part" help:"Print only the steps, or with --all the history, without the \"steps · newest first\" line."`
}

func (ShowCmd) Help() string {
	return `
Open it to learn whether a labelled stage ran in the session, when, and what
was committed since. The view is the session's head, then its steps, newest
first. --head and --no-head print the two apart, for a popup that lays them
side by side; --no-head still opens with any line that says what the view
may lack, so it stands alone.

A prompt is a message you typed or pasted into the session. A request is a
prompt that asked for a labelled skill: a pasted snippet, a slash command,
or words that name the skill.

The head:
  - the title, the short session id and the pane; the directory, the branch,
    the compactions and when the transcript last had a message; and each pull
    request the session opened or linked, with its link. No PR there means
    the reader found none.
  - what the reader could not read or may have missed: "no transcript",
    "transcript unreadable", "3 lines could not be read", or "the reader may
    have missed pull request (claude-steps check)", which says a second count
    of that fact saw more than the reader did. Such a view may lack steps,
    and that fact in particular, so it is no evidence that nothing ran; run
    claude-steps check to see whether the format moved; when it finds no
    drift, the miss is this session's alone. With no transcript the steps
    are your notes, which outlive it.
  - a row per label: when its latest step happened, the commits the session
    made since, for a label that counts them (empty for one that does not),
    and the step; for an envoy round, the time and the count run from its
    dispatch. "·" when the label has no step. Only the session's own git
    commit calls are counted, not the repository's history.
  - your latest notes, three of them unless --all is given.

The steps, under "steps · newest first", are what happened under each label,
each with the request that asked for it, and your notes at the time you wrote
them. The commits between two steps are one count line. The last line says
how many rows the full history (--all) holds, and how many reads of a
skill's file no step shows. A step reads:

  /review  args            a slash command you typed that loaded a skill
  skill review  args       the model called the Skill tool; "failed to load"
                           when the call returned an error
  read skills/x/SKILL.md   the model read a skill's file in answer to a
                           request; a read made to look something up is in
                           the history alone
  pasted <key>             a prompt that holds the opening of the TabType
                           snippet <key>; "in a file" when the snippet came
                           in the file that a prompt "Read <path> and do
                           what it asks." named
  you: "…"                 a prompt that named a labelled skill: your words,
                           not a run
  <request> → <what ran>   what ran, after the request that asked for it. A
                           run with no → is one the reader found no request
                           for, most often asked for in words it does not
                           match; a request on a line of its own is one
                           nothing under the label answered
  review-r1                an envoy round, dated at its dispatch, so the
                           commits above it are ones its reviewer did not
                           read. After the name, what went wrong, if
                           anything: "envoy said <word>", envoy's own word
                           for a job that did not end ok, such as partial or
                           timeout; "collect returned an error"; "run
                           returned an error". envoy's ok says a result came
                           back, not that a review passed, and is not printed
  … (3 times)              the same line under the same labels, at the time
                           of its last

The full history, under "history · newest first", lists every line the
reader found, each with the labels it is under: besides the steps,
"commit  <subject>" for each git commit in a call that returned no error,
"PR #12 opened here" or "linked", "compaction (manual)", and a round's
"dispatched" and "collected" apart. A commit made by a subagent, by git
merge, rebase or cherry-pick, or inside a script is not seen.`
}

func (ShowCmd) HelpSections() []clikit.Section {
	return []clikit.Section{
		{Title: "JSON", Body: `
One object: id, the full session id; pane and where (session:window.pane),
when the session is live in tmux; path, the transcript file; title, cwd, branch and last_at;
status, which is ok, partial, unreadable or missing; unread_lines; events,
oldest first, each with at, kind (skill, read, snippet, mention, round,
commit, pr or compaction) and the fields of its kind; notes, each with at
and text; unread_notes, the note lines that could not be read, and
notes_error when the notes file cannot be read; pull_requests and
compactions, events of those kinds; and labels, each with its name, latest
(the label's latest step, as an event, absent when it has none) and
commits_since (present when the label counts commits). Every time is RFC
3339 with its offset, and none is relative; --all changes nothing here.`},
		{Title: "Sessions", Body: sessions},
		{Title: "Output", Body: colour + `
In the head a Nerd Font glyph marks the pane, the directory, the branch and
each pull request; plain output has no glyphs.`},
	}
}

func (c *ShowCmd) Run(ctx *kong.Context, h host, loader *record.Loader, view render.View) error {
	if c.Session == "" {
		return clikit.Usage(errors.New("show needs a pane or a session id outside tmux"))
	}
	id, pane, err := loader.Target(c.Session, h.panes)
	if err != nil {
		return err
	}
	session := render.Session{Pane: pane, Record: loader.Load(id)}
	out := bufio.NewWriter(ctx.Stdout)
	switch {
	case c.JSON:
		if err := view.ShowJSON(out, session); err != nil {
			return err
		}
	case c.Head:
		view.Head(out, session, c.All)
	case c.NoHead:
		view.Timeline(out, session, c.All)
	default:
		view.Show(out, session, c.All)
	}
	return out.Flush()
}

type BoardCmd struct {
	JSON  bool `xor:"form" help:"Print a JSON array holding each session as show --json prints it."`
	IDs   bool `name:"ids" help:"Start every line with the pane id and the full session id, each followed by a tab."`
	Brief bool `xor:"form" help:"Print each session as its pane, the age of its last message and its title."`
}

func (BoardCmd) Help() string {
	return `
One row per tmux pane that runs a Claude session: the pane, the session's
title, a cell per label, the latest pull request, and your latest note after
the number of notes in parentheses, "(2) skip verify". A pane is found by
its @claude_ctx_sid pane option, which holds the session id: the dotfiles
context chip sets it, and "tmux set-option -p -t %12 @claude_ctx_sid <id>"
sets it by hand. A session in a pane without it is left off, and
"claude-steps show <session id>" still reads it.

--ids is for a picker: the two ids lead every line, are empty on the header
line, and are never coloured. --brief prints no header line. --json gives
every time in RFC 3339, never relative.

A label's cell says when its latest step happened, never how it went. It
reads "[read|pasted] <age> [+N]", "named <age>", or "·":

  <age>    how long ago the label's latest step happened: now, or a number
           of s, m, h, d, w, mo or y ("11m", "2d"). Alone, the step ran: a
           skill loaded or an envoy round dispatched, timed from its dispatch
  +N       the commits the session made since that step started ("2h +1",
           "16m +0"), on a label that counts them and never on one that
           does not
  read     that step was a skill's file read in answer to a request, not a
           load of the skill
  pasted   that step was a pasted snippet, a request nothing under the
           label has answered
  named    nothing is under the label but a prompt that named the skill
  ·        the label has no step in the session

"!" before a title says the transcript was read with something missing, and
the note cell says what, in square brackets, where there is room; show says
it in full. A session whose transcript is gone or unreadable says so in
place of its cells. With no Claude pane, --json prints [] and the text board
prints nothing and says so on stderr; both exit 0.`
}

func (BoardCmd) HelpSections() []clikit.Section {
	return []clikit.Section{{Title: "Output", Body: colour}}
}

func (c *BoardCmd) Run(ctx *kong.Context, h host, loader *record.Loader, view render.View) error {
	all, err := h.panes()
	if err != nil {
		return err
	}
	live := record.Live(all)
	sessions := make([]render.Session, len(live))
	var wg sync.WaitGroup
	for i := range live {
		wg.Go(func() { sessions[i] = render.Session{Pane: &live[i], Record: loader.Load(live[i].SessionID)} })
	}
	wg.Wait()
	if c.JSON {
		return view.BoardJSON(ctx.Stdout, sessions)
	}
	if len(sessions) == 0 {
		fmt.Fprintln(ctx.Stderr, "claude-steps: no tmux pane runs a Claude session")
		return nil
	}
	out := bufio.NewWriter(ctx.Stdout)
	if c.Brief {
		view.Brief(out, sessions, c.IDs)
	} else {
		view.Board(out, sessions, c.IDs)
	}
	return out.Flush()
}

type NoteCmd struct {
	Session string   `arg:"" help:"A pane id such as %12, a session id, or at least its first eight characters."`
	Text    []string `arg:"" help:"The note's words, joined by spaces; put -- before them when any starts with a dash."`
}

func (NoteCmd) Help() string {
	return `
A note is your own dated line on a session, for what its transcript cannot
say. It shows in the session's head, among its steps at the time you wrote
it, and in the board's note cell. Nothing is printed when the note is saved.
The tool only appends; to change or remove a note, edit the session's notes
file by hand (see Files). An import from another machine that still holds
the note brings it back.`
}

func (NoteCmd) HelpSections() []clikit.Section {
	return []clikit.Section{
		{Title: "Examples", Body: `
claude-steps note %12 skip verify, the spike covered it
claude-steps note 9ba78130 -- --force was needed for the rebase`},
		{Title: "Sessions", Body: sessions},
	}
}

func (c *NoteCmd) Run(h host, loader *record.Loader) error {
	text := strings.TrimSpace(strings.Join(c.Text, " "))
	if text == "" {
		return clikit.Usage(errors.New("the note is empty"))
	}
	id, _, err := loader.Target(c.Session, h.panes)
	if err != nil {
		return err
	}
	if err := loader.AddNote(id, h.now(), text); err != nil {
		return fmt.Errorf("the note was not saved: %w", err)
	}
	return nil
}

type CheckCmd struct{}

func (CheckCmd) Help() string {
	return fmt.Sprintf(`
Claude Code treats its transcript format as internal and changes it without
notice, and a reader that no longer fits it shows fewer steps with nothing
to say so. check reads every transcript changed in the last %d days as the
views do (an older one is not read), and counts each fact two ways: by the
reader's own rule, and by a second trace that should exist whenever the
first does, such as a slash command's expansion row or the URL gh printed
for a pull request. It prints how many transcripts it read and what it
could not, then a table with a row per fact: the reader's count, the second
count, the misses, the sessions they were in, and both rules. The facts are
named as a view's "the reader may have missed …" line names them.

Run it after a Claude Code update, before trusting a view. It exits 1 after
the report when the format may have changed: a transcript cannot be read, a
line does not decode, no transcript holds a message the reader recognises,
or the reader missed more than one in %d of a fact's second traces; a
single view may warn of a miss that stays within that over the week. The
reader's rules, in claude-steps' own source, then need fixing, and until
they are, a view may lack the facts the report names, so do not read their
absence as nothing having happened. It exits 1 with nothing on stdout when the transcripts cannot all
be listed.`, record.SurveyWindow/(24*time.Hour), record.MissTolerance)
}

func (c *CheckCmd) Run(ctx *kong.Context, h host, loader *record.Loader) error {
	s, err := loader.Survey(h.now())
	if err != nil {
		return err
	}
	render.Survey(ctx.Stdout, s)
	if len(s.Drift) > 0 {
		return errors.New("check found drift")
	}
	return nil
}

type ImportNotesCmd struct {
	SessionID string `arg:"" help:"The session's full id, as board --ids and show --json print it; a prefix is refused, as this machine may hold nothing yet to match it."`
}

func (ImportNotesCmd) Help() string {
	return `
Stdin is the notes file the other machine keeps for the session, one JSON
note per line ({"at":"<RFC 3339 time>","text":"…"}). The notes this machine
lacks are appended and counted on stdout ("2 notes added"); a note with the
same time and text as one already here is skipped, so importing the same
file twice prints "0 notes added" the second time. A note removed here by
hand comes back if the file still holds it. The transcript need not be on this machine yet. A
line that is not a note is skipped, and the command exits 1 once the rest
are added.`
}

func (c *ImportNotesCmd) Run(ctx *kong.Context, loader *record.Loader, stdin io.Reader) error {
	if !record.IsSessionID(c.SessionID) {
		return clikit.Usage(fmt.Errorf("%q is not a full session id", c.SessionID))
	}
	added, bad, err := loader.ImportNotes(c.SessionID, stdin)
	if err != nil {
		return fmt.Errorf("the notes were not merged: %w", err)
	}
	fmt.Fprintf(ctx.Stdout, "%d notes added\n", added)
	if bad > 0 {
		return fmt.Errorf("%d lines on stdin were not notes and were skipped", bad)
	}
	return nil
}

// host is what a run reads besides its arguments and the environment: the
// clock, tmux's panes, and whether stdout is a terminal. Tests bind their own.
type host struct {
	now      func() time.Time
	panes    func() ([]panes.Pane, error)
	terminal func(io.Writer) bool
}

// newView is how this run prints. A popup reads the output through a pipe,
// so it asks for colour and gives the width in the environment.
func newView(ctx *kong.Context, h host, cfg config.Config) render.View {
	home, _ := os.UserHomeDir()
	v := render.View{Now: h.now(), Home: home, Labels: cfg.Labels}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		v.Width = n
	}
	force := os.Getenv("CLICOLOR_FORCE")
	v.Color = os.Getenv("NO_COLOR") == "" && (h.terminal(ctx.Stdout) || force != "" && force != "0")
	return v
}

// isTerminal reports whether w is a terminal. clikit hands a command
// os.Stdout itself, so the file is there to ask.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

var app = clikit.App{
	Name:        "claude-steps",
	Description: "claude-steps shows what has happened in Claude Code sessions, read from their transcript files.",
	New:         func() any { return &CLI{} },
	Options: []kong.Option{
		kong.Bind(host{now: time.Now, panes: panes.List, terminal: isTerminal}),
		// Read only for a command that runs, so a broken file cannot hide
		// the help.
		kong.BindSingletonProvider(config.Load),
		kong.BindToProvider(func(cfg config.Config) *record.Loader { return record.NewLoader(cfg) }),
		kong.BindToProvider(newView),
	},
}

func main() { os.Exit(app.Run(os.Args[1:])) }
