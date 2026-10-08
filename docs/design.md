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
  transcript reports or something unread, a hue and a glyph mark the kind of
  an item in a session's head, weight marks a count of commits that is not
  zero, and nothing grades a date.
- **Unknown is shown as unknown.** A line that does not decode, a file or a
  directory that cannot be read and a fact the reader may have missed are
  all said at the top of the view, because a silent skip reads as "nothing
  ran". The steps leave rows to the full history and say how many it holds,
  and how many reads of a labelled skill's file only it shows: a label's `·`
  may stand over a stage asked for in words the reader does not match.
- **One reader.** Every command gets a session from `record.Loader.Load`, so
  the board and the session view cannot disagree, and what happened under a
  label is read once, by `record.Steps`, so a label's row and its steps
  cannot either.
- **What happened, not how.** A label's steps count what happened under it,
  each with the request that asked for it. How a round was dispatched and
  collected, and which load of a skill led to it, is the full history's to
  say.

## Flow

```text
tmux pane option @claude_ctx_sid → session id
session id → <id>.jsonl in any project directory → decode → events, signals, read status
session id → notes/<id>.jsonl                             → notes
record, labels → steps → label states, the steps' lines → text or JSON
```

Nothing persists between invocations except the notes.

## Who owns what

- **`internal/record/decode.go`:** every fact about transcript row shapes.
  Claude Code documents the format as internal, so a format change is
  repaired here and nowhere else. Rows are decoded as JSON structurally; no
  rule depends on spacing or key order.
- **`internal/record/rounds.go`:** what a session's Bash calls show of envoy,
  joined into rounds: which round a collect read, which dispatch replaced
  which, and what a call's output proves. The shapes envoy prints live here,
  and nothing else writes a round's fields.
- **`internal/record/commands.go`:** which simple command is a commit, an
  envoy run, an envoy collect or a skill's file printed, and what it names.
  It reads an argv and no transcript row.
- **`internal/shell`:** what a Bash call runs, as simple commands, each with
  its standard input, the directory it runs in, whether it runs only on a
  branch decided at run time (`Guarded`), and whether a pipe, a file or a
  substitution takes what it prints (`Captured`). It parses with
  `mvdan.cc/sh`, so quoting, here-documents and subshell scope follow the
  shell's own rules, and nothing in `record` reasons about shell syntax. A
  directory, a variable and a function hold for the scope that set them. A
  variable assigned on a branch decided at run time reads as its source text
  after the branch, since either value may hold.
- **`internal/record/load.go`:** where transcripts live, the read status, and
  turning what the user typed into a session id.
- **`internal/record/labels.go`:** which labels an event is under
  (`LabelsOf`, `belongs`), and a label's state: its latest step and the
  commits since. A label is a board column with no state, and labels are not
  exclusive.
- **`internal/record/steps.go`:** what happened under each label
  (`Steps`): which request each run, read or round came under, which reads
  are steps, and which runs a round took; and how many labelled reads are
  none (`ReadsLeftOut`). Each label is read on its own and the steps are
  merged by event, so a run under two labels that a round of one takes is
  still a step under the other. Render formats the steps and decides none of
  them.
- **`internal/config`:** which files hold snippets, and what a snippet is
  reduced to: its key and the opening of its text. `loadSnippets` is the one
  reader of TabType's file. The set is built from the configuration alone
  and is the same for every session: no rule looks for a file from a
  session's directory.
- **`internal/notes`:** one file per session id, readable when the transcript
  is gone. It is only ever appended to; reading sorts by time and drops exact
  repeats, so a merge from another machine (`claude-steps import-notes`)
  appends what is missing and can never lose a note written meanwhile.
- **`internal/render`:** pure functions from records to text and JSON; the
  current time, the labels, the width to fill and whether to paint are passed
  in. It works out a session's label states from those labels, so a board's
  header and its cells have one source. It owns the layers of a session
  view, every style and glyph, and the fit to a width. A view is its head
  (what the session is, its labels, the notes) and its timeline, and each
  prints alone. The head fits a narrow width whole: an item folds under its
  text rather than run past, a row's text is cut to its room, and a note is
  one line there, since the steps keep it whole. The timeline gives an
  event's text a floor of room and lets the line run past it, since the
  popup wraps it; a step's request takes the room what ran leaves it. A
  line is built from cells that carry their text and their style apart, so
  alignment measures text alone and the plain output is the painted output
  with the escapes and the head's glyphs removed.
- **The popup:** `~/dotfiles/tmux/.config/tmux/scripts/tmux-steps.sh`. It
  owns fzf, keys and pane switching and shows only what the binary prints.
  It lays one session out as lazygit does: the steps (`show --no-head`) in
  the main panel, the head (`show --head`) above the session list
  (`board --brief`) in a side column. A popup too small for the column shows
  the whole view (`show`) on top, the head first and whole, above the list.
  It reads the binary through a pipe, so it asks for colour and gives each
  call its width. `steps.md` beside it holds its design.

## The model

- **Event:** one dated fact. The kinds are the `Kind` constants in
  `internal/record/record.go`; an event's time is its row's, and for a tool
  call that is the call. An event knows the human prompt it came under
  (`Event.Prompt`): the prompt's own events and what the session did until
  the next one. A task notification starts a turn but no prompt, since a run
  that answers a paste often comes after the background round the paste
  started; a slash command the user typed is a prompt.
- **Round:** an envoy job as the transcript shows it: a dispatch, a collect,
  or both. A collect belongs to the round its name read when its command
  ran, and takes its own block of the call's output, so a call's collects
  each keep their own status and a dispatch later in the call is a new
  round. A dispatch under a name whose round is still waiting for a collect
  replaces that round (`Redispatched`, `Event.Waiting`) and counts the
  dispatches it stands for.
- **Label state:** the label's latest step that did not fail, and the
  commits made after it started. For a round that start is the dispatch: a
  reviewer reads the code as it stood then. A round whose run returned an
  error did not fail once it was collected: a result came back. A prompt that only names a skill counts
  when nothing else matches, and never gets a commit count. A paste counts
  as a run does: it dates the label and starts the count. It is a request
  all the same, so the board's cell says `pasted`, as it says `read`.
- **Paste:** a prompt that holds a snippet's opening, typed as text or as a
  slash command with its arguments. As a command that loaded its skill it is
  the paste and the run, each an event at that time. Under a label that
  lists the command's skill the run is the prompt's one line, and under any
  other label that lists its key the paste is a request. A pasted prompt,
  or a paste typed as a command, is not read for the skills it names.
- **Request:** a paste, or a prompt that names a labelled skill; a slash
  command the user typed is a request and its run at once, and the words
  typed after it ask for the other labelled skills they name. A one-word
  name is ordinary English, so it counts only as `/review`, as its
  `skills/review/SKILL.md` or as "review skill"; another file in its
  directory, such as the handoff skill's pickup files a brief names, asks
  for nothing. A request under a label is answered by the first run, read or
  round under that label in the same human prompt, or by the slash command
  typed in the next one. It is then said on every run and dispatched round
  under the label in the answering step's prompt, and a typed command that
  answered nothing is its own prompt's request the same way: the `→` is the
  only thing the steps say about asking, so a run or a round with none is
  one the reader found no request for. A failed call and a round collected
  from elsewhere carry none. The same request sent again before an answer is
  one. A request with no known prompt (a transcript that marks none as the
  user's) is answered by nothing.
- **Step:** what happened under a label: a run or a round, with its
  prompt's request; a read that answered a request; or a request nothing
  answered; and the user's notes. A read is a step only as that answer: the
  model prints a skill's file to look something up as often as to run a
  skill asked for in prose, and a lookup would date a stage nobody ran.
  A session view lists the steps newest first, with the commits between two
  as one count line, under a heading that says the order, since a list read
  from the top otherwise reads as the order things ran in. Every other line
  is in the full history (`show --all`). The view is opened to ask whether a
  labelled step happened and what was committed since, and the steps are
  that answer in order.
- **A round is one step.** Under a label that lists rounds, its lines count
  the rounds run. The line is dated at the dispatch, like the label, so the
  commits above it are the ones its reviewer did not read. Every round such
  a skill runs is dispatched, so the label's runs and reads since its last
  round are this round's and have no line: the round says the latest request
  among them or made in its own prompt, and with none the model's words for
  the latest run. The join is by order alone. It says what went wrong, if
  anything: a run or a collect that returned an error, envoy's word for a job
  that did not end `ok`. A replaced dispatch has no line. The full history
  keeps the dispatch, the collect, the runs and the requests apart.
- **Read status:** `ok`, `partial`, `unreadable`, `missing`. `missing` says
  every directory that could hold the transcript was looked in; one that
  could not be is `unreadable`, with its path. Neither renders as an empty
  timeline: such a session's steps are its notes, which outlive the
  transcript.
- **Signals:** each fact is counted by the reader's rule and by a second
  trace that should exist whenever the first does. A second trace with no
  match is a miss, and so is an envoy call whose job neither its text nor
  its output names. The view says so on the session when the fact may be
  missing from it, and `check` sums a week of transcripts. `check` also fails
  on any line that does not decode: Claude Code writes whole lines, so one it
  could not have written in the shape the reader knows means the shape moved.
  It fails too when the transcripts cannot all be listed: it would report no
  drift over what it never read.

## Traps the code cannot show

- **A slash command is a skill when the next user row is its expansion,**
  which opens with "Base directory for this skill:". A built-in such as
  `/init` answers with a meta prompt of its own, and a `Skill` call later in
  the same turn shares the command's `promptId`.
- **Compaction summaries have no `origin` and name skills.** Rows without an
  origin are the user's only in a transcript where no row marks an origin or
  a source. A session a program started through the Agent SDK marks every
  prompt's source as `sdk` and none's origin, and none of its prompts is the
  user's. The second trace for human prompts takes any source but `sdk` and
  `system` as the user's, so a new way of marking typed prompts fails `check`
  where the steps would lose them in silence.
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
- **A skill is loaded with no tool that names it.** Asked in prose to run a
  skill, the model often prints its file with `cat` and calls neither the
  Skill nor the Read tool (`docs/EVIDENCE.md`), so a `cat` whose output is
  the call's own is a read, and a step when a request asked for it. Nothing
  in the output proves it: `cat -n` numbers the file's opening lines, and
  Claude Code sets a long output aside and keeps its start. The read counts on the call's success, a guarded `cat`
  never counts, and a missing file followed by `; true` counts: that is a
  stated limit.
- **A run's failure comes from the call and from envoy's own lines.** envoy
  prints `job: <dir>` once the job exists and exits non-zero when the job
  ends in anything but `ok`. An error after a job line is therefore a round
  a collect can still read; an error with no job line ran nothing, replaced
  no round, and leaves the name reading what it read before. A job envoy
  says ended `ok` did not fail because a later command of the call did. A
  background dispatch prints nothing, so a run on a branch decided at run
  time, and one envoy refused in a call that returned no error, read as
  dispatched: that is a stated limit.
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
  name too, since a background dispatch never shows its path. An earlier
  dispatch under the name can therefore never show a collect, and sessions
  do dispatch a name twice (`docs/EVIDENCE.md`).
- **A collect's job is not always in the command text.** A loop collects
  `"$j"`, and a path read from a file arrives as `"$(…)"`; `internal/shell`
  keeps such a word as its source text. The reader names the job from the
  blocks envoy printed, and counts a miss when there are none. A fan-out's
  own block names the round; its members' blocks are not rounds.
- **Claude Code wraps pasted text in `<pasted_content id="…">`.** The tag is
  not the user's words, so a mention drops it. A wrapped paste is text: the
  slash command a snippet opens with does not run, and the model calls the
  Skill tool itself. Unwrapped, the same snippet is that command, and the
  rest of its words are the arguments. Which happens is not the snippet's
  choice (`docs/EVIDENCE.md`), so both are matched.
- **A snippet is matched by the opening its file holds today.** Rewording the
  first 80 characters loses the pastes of the earlier text; an edit past
  them loses none. The match is on words alone, so a prompt typed by hand
  with a snippet's opening reads `pasted`. A file that keeps a retired
  wording under its key can be named in `snippets`.
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
- **The head's glyphs are a Nerd Font's.** The terminals in use carry one and
  measure each glyph one column, as `internal/render` does; whatever reads
  plain output may not, so the glyphs are drawn only with the colour.

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
- **Saying how a round ran** ("no collect seen", "dispatched 2 times", a
  board column for the rounds with no collect): the steps are read for what
  happened, and a dispatch the transcript shows no collect of is most often
  one replaced or collected elsewhere (`docs/EVIDENCE.md`). The full history
  keeps both.
- **A request joined to the next run by order alone:** a prompt that names
  a skill in passing would be said on a run hours and prompts later
  (`docs/EVIDENCE.md`). The prompt bounds the join; a round's runs are
  joined by order, since the skill dispatches every round it runs.
- **The steps read in render beside the label states in record:** the two
  answered "what happened under this label" apart, and a run a round of one
  label took vanished from another label's steps while its row still showed
  it.
- **A short timeline that keeps every event but the commits:** the view is
  opened for the labelled steps, and the whole history is one flag away.
- **A pull request among the steps:** what is asked of one is whether the
  work has it yet, and its link. The header answers both, from the link row
  Claude Code writes for the session, and a header with no `PR` says there
  is none.
- **A step each for a round's dispatch, its collect, the skill run before it
  and the request:** the lines under a label then do not count its rounds,
  and the steps run half as long again for the same facts
  (`docs/EVIDENCE.md`).
- **Asking envoy's job line of every dispatch, or leaving out a run on a
  branch:** a background dispatch prints no line, and loops dispatch real
  rounds. A dispatch that did not happen is a round among the steps, which
  sends the user to look (`docs/EVIDENCE.md`).
- **Any command that names a skill's file as a read** (`sed -n`, `head`,
  `grep`): each shows a passage, and together they run more often than `cat`
  prints the file. A lookup after a run would become the label's latest
  event and restart its commit count (`docs/EVIDENCE.md`).
- **A read no request answered, kept among the steps with a mark:** a step
  dates its label, so the read would still date a stage nobody ran, or the
  label's row and the steps would disagree.
- **A mark on a run or a round that answered no request** ("unasked", a
  glyph, a hue): the reader knows a request only by a skill's name, and most
  rounds with none were asked for in prose (`docs/EVIDENCE.md`), so the mark
  would be a verdict, wrong on most of the lines it sat on.
- **A phrase around a one-word skill name as a request** ("update the
  handoff"): the name is ordinary English, so the phrase would pair requests
  with runs they did not ask for. A read asked for in such words stays in
  the history.
- **A project's snippet file found from the session's directory:** a session
  outlives its worktree, so a transcript would read as a paste while the
  work was open and as a mention after the merge (`docs/EVIDENCE.md`). The
  reader would also keep a copy of TabType's rules for a project root and
  for a worktree's primary checkout.
- **A bare date for a paste in a label's cell:** under a label that lists a
  skill it reads as the skill having run.
- **A pasted prompt read for skill names too,** so that a label listing the
  skill and not the key shows `named`: every paste becomes a second row of
  the full history.
- **A popup with every session's board row on top and the session view
  under it:** the popup is opened to read the session it was opened from,
  and a row of label cells reads as a grid of stages, not as the order the
  steps ran in. The board stays for a terminal.
- **A note folded in the head:** beside the steps the head is a narrow
  column of fixed height, so a long note would take rows from the session
  list, and the steps beside it fold each note whole.
- **A block of history under each label, or a lane per label:** the first
  loses the order across labels, the second has no room for an event's text.
- **A row drawn faint when no label has an event:** such a session can hold
  commits or notes.
- **Colours taken from the tmux palette:** they tie the binary to tmux and
  leave a plain shell without them. The terminal's own 16 colours follow the
  theme everywhere.

## Contracts other repositories depend on

- **`board --ids` rows:** `<pane id> TAB <session id> TAB <text>`, both ids
  empty on the header line. The popup parses them from `board --ids --brief`,
  which has no header line. The text may carry escape codes; the ids never
  do.
- **`show --head`, `show --no-head [--all]` and `show [--all]`:** the
  popup's status, its main panel, and the stacked panel that holds both.
  `show --no-head` opens with what the view may lack, so the main panel
  stands on its own.
- **The environment the popup sets:** `CLICOLOR_FORCE=1` asks for colour
  through a pipe and `COLUMNS` is the width a line may fill; `NO_COLOR` wins
  over both.
- **Text the popup test waits on:** the head's first line as the popup
  paints it, `<title>   <short id>   <glyph> <pane>` (the id and the pane
  start the next line when the title fills the width), a label row in its
  brief form, the brief list's rows cut to the side column, the newest step
  on the main panel's first line, the panels' labels, and on the stacked
  panel the head's `notes` row and the `history · newest first` heading.
- **The notes file and `import-notes`:**
  `$XDG_STATE_HOME/claude-steps/notes/<id>.jsonl`, one JSON note per line.
  `claude-tomini` sends a moved session's file to `claude-steps import-notes`
  on the mini.
- **The configuration keys:** the labels, the hue each may name and the
  snippet files live in the dotfiles `claude-steps` package. A binary
  refuses a key it does not know and a value of another type, so a new one
  reaches a machine with the binary that reads it, or after it.
- **`@claude_ctx_sid`:** set by the dotfiles context chip; without it the
  board is empty.

## Changing it

- **Claude Code changed a row shape:** fix the rule in `decode.go`, add the
  shape to `internal/fixture`, pin it with a case in
  `internal/record/record_test.go`, then run `claude-steps check`.
- **envoy changed what it prints or takes:** the output's shapes are in
  `internal/record/rounds.go` and the argv grammar in
  `internal/record/commands.go`; pin the shape with a case in
  `internal/record/rounds_test.go`.
- **A project gains snippets of its own:** name its file in `snippets` and
  list its keys under their labels, both in the dotfiles package. A key no
  label lists is a row of the full history and never a step.
- **A new kind of fact:** an event kind, its rule, a second trace for it, its
  line in `render`, and its line in the README.
- **A new line or cell in a view:** build it from cells with a style, never
  a string that holds an escape. `TestColourIsTheSameTextPainted` holds the
  painted and the plain output to one text, `TestBoard` pins the grammar of a
  cell, `TestTheBoardFitsTheWidth` holds the label cells whole at any
  width, and `TestTheHeadFitsTheSideColumn` holds the head inside the
  popup's narrowest side column. A cell that says more is wider on every
  row of its column, and `TestTheBoardClosesUpBeforeItRunsOver` holds what
  gives way when the cells alone are too wide: the space between columns,
  never a cell.

## The build's record

`docs/specs/2026-10-02-session-view.md` holds what this page leaves out:
the measured premises behind each rule, and the numbered obligations the
tests cite. `docs/specs/2026-10-05-project-snippets.md` holds the same for
the snippet files and the paste. `docs/EVIDENCE.md` holds what the views
showed for real sessions: each pass's counts, what it changed, and what the
next should compare.
