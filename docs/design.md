# Design

`claude-steps` answers "what has run in this session, and what was committed
since" from outside the session. Asking the session costs it a turn and can
set its model working; a reader of the transcript file costs nothing and
tells the model nothing.

## Stands

- **No handle on a session.** Transcripts are opened read-only, the only
  writes are the user's notes, the only process started is `tmux list-panes`,
  and the binary links no network package. Any write path into a session is a
  way to change what its model reads.
- **Dated facts, never verdicts.** A line says what the transcript holds and
  when. An invocation does not prove a check finished or still covers the
  code, so nothing prints a tick, "done" or "stale"; the user judges. Colour
  is under the same stand: a hue names a label, red marks an error the
  transcript reports or something unread, weight marks a count of commits
  that is not zero, and nothing grades a date.
- **Unknown is shown as unknown.** A line that does not decode, a file that
  cannot be read and a fact the reader may have missed are all said at the
  top of the view, because a silent skip reads as "nothing ran". The steps
  leave rows to the full history and say how many it holds.
- **One reader.** Every command gets a session from `record.Loader.Load`, so
  the board and the session view cannot disagree.

## Flow

```text
tmux pane option @claude_ctx_sid → session id
session id → <id>.jsonl in any project directory → decode → events, signals, read status
session id → notes/<id>.jsonl                             → notes
record, labels → label states, each event's labels → text or JSON
```

Nothing persists between invocations except the notes.

## Who owns what

- **`internal/record/decode.go`:** every fact about transcript row shapes.
  Claude Code documents the format as internal, so a format change is
  repaired here and nowhere else. Rows are decoded as JSON structurally; no
  rule depends on spacing or key order.
- **`internal/shell`:** what a Bash call runs, as simple commands, each with
  its standard input, the directory it runs in, and whether it runs only on
  a branch decided at run time (`Guarded`). It parses with `mvdan.cc/sh`, so
  quoting, here-documents and subshell scope follow the shell's own rules,
  and nothing in `record` reasons about shell syntax.
- **`internal/record/load.go`:** where transcripts live, the read status, and
  turning what the user typed into a session id.
- **`internal/record/labels.go`:** which labels an event is under
  (`LabelsOf`), a label's latest event and the commits since. A label is a
  board column with no state, and labels are not exclusive: a label's row and
  a session's steps read the one membership rule.
- **`internal/notes`:** one file per session id, readable when the transcript
  is gone. It is only ever appended to; reading sorts by time and drops exact
  repeats, so a merge from another machine (`claude-steps import-notes`)
  appends what is missing and can never lose a note written meanwhile.
- **`internal/render`:** pure functions from records to text and JSON; the
  current time, the width to fill and whether to paint are passed in. It owns
  the layers of a session view, which lines are steps, every style, and the
  fit to a width. A line is built from cells that carry their text and their
  style apart, so alignment measures text alone and the plain output is the
  painted output with the escapes removed.
- **The popup:** `~/dotfiles/tmux/.config/tmux/scripts/tmux-steps.sh`. It
  owns fzf, keys and pane switching and shows only what the binary prints.
  It reads the binary through a pipe, so it asks for colour and gives each
  call its width.

## The model

- **Event:** one dated fact. The kinds are the `Kind` constants in
  `internal/record/record.go`; an event's time is its row's, and for a tool
  call that is the call.
- **Label state:** the latest matching event that did not fail, and the
  commits made after it started. For a round that start is the dispatch: a
  reviewer reads the code as it stood then; the view prints the collect at
  its own time. A prompt that only names a skill counts when nothing else
  matches, and never gets a commit count.
- **Step:** an event under a label, or a note. A session view lists the
  steps newest first, with the commits between two as one count line; every
  other line is in the full history (`show --all`). The view is opened to ask
  whether a labelled step ran and what was committed since, and the steps
  are that answer in order.
- **Uncollected round:** a round this session dispatched for which the
  transcript holds no collect. It is listed apart from the labels, because a
  label state keeps only its latest event and a round no label lists has no
  row. The words are "no collect seen": the round may be running, collected
  from another session, or given up on.
- **Read status:** `ok`, `partial`, `unreadable`, `missing`. The last two
  never render as an empty timeline.
- **Signals:** each fact is counted by the reader's rule and by a second
  trace that should exist whenever the first does. A second trace with no
  match is a miss. The view says so on the session when the fact may be
  missing from it, and `check` sums a week of transcripts. `check` also fails
  on any line that does not decode: Claude Code writes whole lines, so one it
  could not have written in the shape the reader knows means the shape moved.

## Traps the code cannot show

- **A slash command is a skill when the next user row is its expansion,**
  which opens with "Base directory for this skill:". A built-in such as
  `/init` answers with a meta prompt of its own, and a `Skill` call later in
  the same turn shares the command's `promptId`.
- **Compaction summaries have no `origin` and name skills.** Rows without an
  origin are the user's only in a transcript where no row carries one.
- **The Bash tool runs zsh here, which does not split an unquoted variable.**
  `C="git commit -q"; $C -m x` runs nothing. A function defined in the call
  does run where it is called, with the call's here-document as its input.
- **The parser returns `f() { … } && b` as one function body.** bash runs `b`
  once `f` is defined, so `funcBody` in `internal/shell` splits the chain.
- **A commit's success comes from the whole call.** Most commits run with
  `-q` and print nothing, so a call that returned no error is the evidence.
  An error means no commit unless git's own `[branch sha]` line is in the
  output, and a commit `internal/shell` marks guarded (after `||`, in an
  `if`, `case` or loop, in the background) needs that line too. A failed
  commit followed by `; true` counts: that is a stated limit.
- **envoy's `ok` means the job returned a result.** Beside a review it would
  read as "passed", so it is not printed. `collect --status-only` is a probe,
  and a collect that prints no status keeps the word already read.
- **A second trace is evidence, not a superset.** Claude Code itself leaves
  the odd created pull request without its link row, so `check` fails a fact
  only past one miss in ten. The view shows such a pull request from the URL
  gh printed and does not warn about it.
- **envoy runs every dispatch in a directory of its own** (`review-r1`, then
  `review-r1+2`). A collect by name means the session's latest dispatch, as
  envoy reads it; a collect by path joins the latest dispatch under that
  name too, since a background dispatch never shows its path.
- **The terminal draws bold in a colour of the theme's own.** A hue under
  bold is lost, so no span is both; emphasis is weight alone.
- **Yellow does not carry text on a light background** (2.16:1 on one of the
  themes in use), so a label takes it only by naming it.
- **A CJK character takes two columns.** Text is measured in screen columns,
  with the width rules fixed in `internal/render` so the output does not
  depend on the locale.
- **Transcript lines exceed a megabyte.** The reader uses no scanner with a
  fixed line limit.
- **Claude Code sets copies aside under longer names.** The transcript is
  found by its whole file name.

## Alternatives this design beats

- **A recorder inside the session** (a mod): its commands write rows the
  model reads, and it only knows sessions started after it loaded.
- **A state per check, with user marks:** a load is not an outcome, and a
  mark outranks later evidence. Notes cover the correction case.
- **A key-driven screen inside the binary:** it cannot be tested with
  fixtures, and the dotfiles popups already use fzf.
- **envoy's job store or a session index as the source:** the transcript
  already holds each round, and a second source is a second format to track.
- **A "verified through version" constant:** Claude Code updates most days,
  so the warning would always show; the signals speak only when there is
  drift.
- **Hue by state,** a traffic light on the commits since: a verdict made with
  colour.
- **A word in a label's cell for a round with no collect** ("out"): it claims
  a present state the transcript cannot show, an older such round hides
  behind the label's latest event, and a round no label lists has no cell.
- **A short timeline that keeps every event but the commits:** the view is
  opened for the labelled steps, and the whole history is one flag away.
- **A block of history under each label, or a lane per label:** the first
  loses the order across labels, the second has no room for an event's text.
- **A row drawn faint when no label has an event:** such a session can hold
  commits, notes, or a round with no collect.
- **Colours taken from the tmux palette:** they tie the binary to tmux and
  leave a plain shell without them. The terminal's own 16 colours follow the
  theme everywhere.

## Contracts other repositories depend on

- **`board --ids` rows:** `<pane id> TAB <session id> TAB <text>`, both ids
  empty on the header line. The popup parses them. The text may carry escape
  codes; the ids never do.
- **`show --all`:** what the popup's preview runs for a session's history.
- **The environment the popup sets:** `CLICOLOR_FORCE=1` asks for colour
  through a pipe and `COLUMNS` is the width a line may fill; `NO_COLOR` wins
  over both.
- **Text the popup test waits on:** a session view's first line,
  `<title>   <short id>   <pane>`, and its `steps` and `history` headings.
- **The notes file and `import-notes`:**
  `$XDG_STATE_HOME/claude-steps/notes/<id>.jsonl`, one JSON note per line.
  `claude-tomini` sends a moved session's file to `claude-steps import-notes`
  on the mini.
- **The configuration keys:** the labels, and the hue each may name, live in
  the dotfiles `claude-steps` package.
- **`@claude_ctx_sid`:** set by the dotfiles context chip; without it the
  board is empty.

## Changing it

- **Claude Code changed a row shape:** fix the rule in `decode.go`, add the
  shape to `internal/fixture`, pin it with a case in
  `internal/record/record_test.go`, then run `claude-steps check`.
- **A new kind of fact:** an event kind, its rule, a second trace for it, its
  line in `render`, and its line in the README.
- **A new line or cell in a view:** build it from cells with a style, never
  a string that holds an escape. `TestColourIsTheSameTextPainted` holds the
  painted and the plain output to one text, `TestBoard` pins the grammar of a
  cell, and `TestTheBoardFitsTheWidth` holds the label cells whole at any
  width.

## The build's record

`docs/specs/2026-10-02-session-view.md` holds what this page leaves out:
the measured premises behind each rule, and the numbered obligations the
tests cite.
