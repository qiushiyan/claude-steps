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
  code, so nothing prints a tick, "done" or "stale"; the user judges.
- **Unknown is shown as unknown.** A line that does not decode, a file that
  cannot be read and a fact the reader may have missed are all said on the
  view, because a silent skip reads as "nothing ran".
- **One reader.** Every command gets a session from `record.Loader.Load`, so
  the board and the session view cannot disagree.

## Flow

```text
tmux pane option @claude_ctx_sid → session id
session id → <id>.jsonl in any project directory → decode → events, signals, read status
session id → notes/<id>.jsonl                             → notes
record → label states → text or JSON
```

Nothing persists between invocations except the notes.

## Who owns what

- **`internal/record/decode.go`:** every fact about transcript row shapes.
  Claude Code documents the format as internal, so a format change is
  repaired here and nowhere else. Rows are decoded as JSON structurally; no
  rule depends on spacing or key order.
- **`internal/shell`:** what a Bash call runs, as simple commands. It keeps
  quoted text, here-documents and commit messages from counting as commands.
- **`internal/record/load.go`:** where transcripts live, the read status, and
  turning what the user typed into a session id.
- **`internal/record/labels.go`:** a label's latest event and the commits
  since. A label is a board column with no state.
- **`internal/notes`:** one append-only file per session id, readable when
  the transcript is gone.
- **`internal/render`:** pure functions from records to text and JSON, with
  the current time passed in.
- **The popup:** `~/dotfiles/tmux/.config/tmux/scripts/tmux-steps.sh`. It
  owns fzf, keys and pane switching and shows only what the binary prints.

## The model

- **Event:** one dated fact. The kinds are the `Kind` constants in
  `internal/record/record.go`; an event's time is its row's, and for a tool
  call that is the call.
- **Label state:** the latest matching event that did not fail, and the
  commits made after it started. For a round that start is the dispatch: a
  reviewer reads the code as it stood then. A prompt that only names a skill
  counts when nothing else matches, and never gets a commit count.
- **Read status:** `ok`, `partial`, `unreadable`, `missing`. The last two
  never render as an empty timeline.
- **Signals:** each fact is counted by the reader's rule and by a second
  trace that should exist whenever the first does. A second trace with no
  match is a miss. The view says so on the session, and `check` sums a week
  of transcripts.

## Traps the code cannot show

- **A slash command is a skill when the next user row is its expansion.** A
  shared `promptId` is not enough: a `Skill` call later in the same turn
  carries it too.
- **Compaction summaries have no `origin` and name skills.** Rows without an
  origin are the user's only in a transcript where no row carries one.
- **The Bash tool runs zsh here, which does not split an unquoted variable.**
  `C="git commit -q"; $C -m x` runs nothing. A function defined in the call
  does run where it is called, with the call's here-document as its input.
- **A commit's success comes from the whole call.** An error means no commit
  unless git's own `[branch sha]` line is in the output. A failed commit
  followed by `; true` counts: that is a stated limit.
- **envoy's `ok` means the job returned a result.** Beside a review it would
  read as "passed", so it is not printed. `collect --status-only` is a probe,
  and a collect that prints no status keeps the word already read.
- **A second trace is evidence, not a superset.** Claude Code itself leaves
  the odd created pull request without its link row, so `check` fails a fact
  only past one miss in ten.
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

## Contracts other repositories depend on

- **`board --ids` rows:** `<pane id> TAB <session id> TAB <text>`, both ids
  empty on the header line. The popup parses them.
- **The notes file:** `$XDG_STATE_HOME/claude-steps/notes/<id>.jsonl`, one
  JSON object per line opening with its UTC time. `claude-tomini` merges two
  such files by sorting their lines.
- **The configuration keys:** the labels live in the dotfiles
  `claude-steps` package.
- **`@claude_ctx_sid`:** set by the dotfiles context chip; without it the
  board is empty.

## Changing it

- **Claude Code changed a row shape:** fix the rule in `decode.go`, add the
  shape to `internal/fixture`, pin it with a case in
  `internal/record/record_test.go`, then run `claude-steps check`.
- **A new kind of fact:** an event kind, its rule, a second trace for it, its
  line in `render`, and its line in the README.

## The build's record

`docs/specs/session-view.md` holds what this page leaves out: the measured
premises behind each rule, and the numbered obligations the tests cite.
