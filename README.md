# claude-steps

Shows what has happened in Claude Code sessions, read from their transcript
files: which skills ran, which review and consult rounds were dispatched and
collected, what was committed since, the pull requests, the compactions, and
your own notes.

It reads tmux and the transcripts and writes only your notes. It never writes
to a session and makes no model call, so asking it costs the session nothing
and tells the model nothing.

```text
$ claude-steps show
The calendar walks days once   9ba78130   work:2.1
~/dev/app   feat/calendar   1 compaction, last 2 hours ago   last message 1 minute ago
PR #7145 opened here  https://github.com/acme/app/pull/7145
────────────────────────────────────────────────────────────────────────
review   2 hours ago      1 commit since    review-r2
verify   15 minutes ago   0 commits since   skill verify-local spikes
docs     ·

notes   1 hour ago   skip the docs pass

steps · newest first
  verify   15m   skill verify-local  spikes
  note     1h    skip the docs pass
                 1 commit
  review   2h    review-r2
                 1 commit
  review   3h    pasted review-verify → review-r1

11 rows in the full history (show --all)

$ claude-steps board
pane      session                       review  verify  docs      PR     note
work:2.1  The calendar walks days once  2h +1   15m +0  ·         #7145  skip the docs pass
work:2.2  Split the importer            ·       ·       named 3h  ·
```

The view states dated facts. It does not say a check is finished or still
covers the code: "review, 2 hours ago, 1 commit since" is for you to judge.

## Commands

```text
claude-steps show [<pane>|<session>] [--all] [--json|--head|--no-head]   one session: its labels, notes and steps
claude-steps board [--json] [--ids] [--brief]                           every Claude pane in tmux, one row each
claude-steps note <pane>|<session> <text…>                              append a note to a session
claude-steps check                                                      test the reader against recent transcripts
claude-steps import-notes <session id>                                  merge notes from another machine, read on stdin
```

`<pane>` is a tmux pane id such as `%12`; `show` defaults to the pane it runs
in. `<session>` is a session id or its first eight or more characters; a
live pane's full session id works before the session has a transcript.
`claude-steps --help` has the rest.

`board` finds Claude panes through the tmux pane option `@claude_ctx_sid`.
Without something setting it, the board is empty and `show <session id>`
still works.

The tmux popup (`prefix S`) shows one session's steps beside its status and
a list of the other sessions; a popup too small for that shows the status
first, whole, with the steps under it. `show --no-head` prints the steps alone, or
with `--all` the history, with no heading; `show --head` prints what comes
before them; and `board --brief` prints each session as its pane, the age of
its last message and its title, with no header line.

## Reading a view

A session view reads from the top, newest first:

- **The header:** the title, the session id and its pane; then the directory,
  the branch, the compactions and when the transcript last had a message; then
  each pull request the session opened or linked with its link, newest first.
  No `PR` there says the session has none. A transcript that could not be read
  in full says so on the next line.
- **The labels:** one row for each, always: when its latest step happened,
  the commits made since where the label counts them, and the step. For a
  round the time and the count run from its dispatch. `·` says the label
  has no step in the session; a read nothing asked for is in the history.
- **`notes`:** your latest notes, one line each, cut where the width is
  known. `show --all` lists every one.
- **`steps · newest first`:** what happened under each label, and your notes
  at the time you wrote them, each whole. A step is what ran, said with the
  request its prompt made, a skill's file read in answer to a request, or a
  request nothing answered. A round is one
  step, at its dispatch, and takes the skill's runs before it, so the
  `review` lines count the review rounds. The commits between two steps are
  one count line, and so are those after the newest step and before the
  oldest; the commits above a round were made after its dispatch. A run of
  the same line under the same labels is one line, at the time of its last,
  with `(3 times)`. The last line says how many rows the full history
  holds.
- **`show --all`** prints that history in the steps' place: every line below,
  each with the labels it is under. With no label configured, `show` prints
  it too.

What a line says:

- **`/review  args`:** a slash command you typed that loaded a skill.
- **`skill review  args`:** the model called the Skill tool; `failed to load`
  when the call returned an error.
- **`read skills/x/SKILL.md`:** the model read a skill's file, as it does
  when a prompt names the skill or points at the path: a Read call, or a
  `cat` that printed the file in a call that returned no error. Among the
  steps it is always the answer to a request; a read the model made to look
  something up is in the history alone. A `cat` that may have been skipped,
  or whose output went to a pipe or a file, is not a read, and neither is a
  passage shown by `sed`, `head` or `grep`.
- **`pasted <key>`:** a prompt holding the opening of a TabType snippet from
  a file `snippets` names. A prompt typed by hand with the same opening reads
  the same. A snippet that opens with a slash command and arrives as that
  command is the `/review  args` line and a `pasted` line at one time, and
  the paste is a step only under a label the run is not under.
- **`you: "…"`:** a prompt that named a labelled skill, or a slash command
  whose words named another: `/review` counts, and so does
  `skills/review/SKILL.md` or "review skill"; a hyphenated name counts as a
  word. Your words, not a run.
- **`<request> → <what ran>`:** a paste, a prompt that named the skill, or a
  slash command you typed, and a step under the same label that its prompt
  asked for: the first run or round made in the same prompt, which answers
  it, or the slash command you typed next, and every run and round after it
  in that prompt. A run or a round with no `→` is one no request was found
  for; a failed call and a round collected from elsewhere carry none. On a
  line of its own a request is one nothing under the label answered there;
  the same request sent again before an answer is one. A transcript that
  marks no prompt as yours answers no request, and a session a program
  started has no prompt of yours. Where the width is short the request
  gives way to what ran, and is left out before what ran is cut.
- **`review-r1`:** a round among the steps, at its dispatch. It takes the
  label's skill runs and reads since its last round, since every round the
  skill runs is dispatched, and says the latest request among them or made
  in its own prompt: `/review args → review-r1`, and the same for each round
  that prompt ran. With no request it says
  the model's own run: `review-r1  skill review  codex`. Runs and rounds are
  joined by their order alone. What went wrong is said after the name:
  - **`envoy said partial`:** envoy's status for the job when it is not
    `ok`. `ok` says a result came back, not that a review passed.
  - **`collect returned an error`:** the collect call failed.
  - **`run returned an error`:** the call that ran envoy failed, and envoy
    did not say the job ended `ok`. The round keeps a line of its own, with
    envoy's word when a collect followed: `run returned an error, envoy said
    timeout`.
- **`review-r1  dispatched` / `collected`:** the same round in the full
  history, where the dispatch, the collect, the skill run and the request
  each have a line at their own time.
- **`commit  subject`:** a `git commit` in a call that returned no error. A
  commit that may have been skipped (after `||`, inside an `if` or a loop,
  in the background) counts only when git printed its `[branch sha]` line.
- **`PR #12 opened here` / `linked`:** a pull request Claude Code linked to
  the session; opened here only when `gh pr create` in this session returned
  its URL. The header gives its link, the full history its repository.
- **`compaction (manual)`.**

On the board a label's cell is the time of its latest event (`11m`, `2d`);
for a round that is the dispatch. `+2` counts the commits made since that
event started; `read`, `pasted` or `named` in front says the latest event was
only a file read in answer to a request, only a pasted snippet, or only a
prompt that named the skill; `·` says the label has no step.

A transcript that cannot be read says so (`no transcript`,
`transcript unreadable`, `3 lines could not be read`) and is never drawn as an
empty timeline: its steps are your notes, which outlive it. `no transcript`
says every project directory was looked in; one that could not be is
`transcript unreadable`, with its path, and `claude-steps check` fails. `the reader may
have missed …` says a fact may be absent from the view: Claude Code's
transcript format may have moved, or an envoy call took its job from a
variable or a file and printed no `job:` line. `claude-steps check` says
whether the misses amount to drift. On the board `!` before a title marks such
a session, and the words are in its note cell where there is room.

Colour names and never grades. Each label's name has a hue; red marks an
error the transcript reports and what the reader could not read; a count of
commits is bold when there are any. In the header the pane, the directory,
the branch and each pull request carry a Nerd Font glyph and a hue of their
own, and the title is bold. Output is coloured on a terminal, or through a
pipe with `CLICOLOR_FORCE=1`, and never with `NO_COLOR` set; plain output has
no glyphs.

With `COLUMNS` set, a view fits that width: an event's text is cut to one
line, and a note in the steps is folded onto the lines under it. The part
before the steps fits whole: what does not fit a line starts the next, the
pull requests start a line of their own when the header does not fit one, a
link, a path or a warning wider than the width is folded and never cut, and
below 80 columns a row gives its time and its count as the board's cells do
(`8m`, `+1`). On the board the title and the note give way while the label
cells keep theirs. A note with no room is left to the session view, and where
the cells alone leave the title no room the columns close up to one space.

Not shown: commits made by a subagent, by `git merge`, `rebase` or
`cherry-pick`, or inside a script.

## Configuration

`~/.config/claude-steps/config.toml`, optional. The binary ships no labels;
with no file the board shows sessions, pull requests and notes, and `show`
prints the whole timeline.

```toml
projects_dir = "~/.claude/projects"            # default
snippets = [                                   # default: the first file alone
  "~/.config/tabtype/config.toml",
  "~/dev/app/.tabtype.local.toml",             # a project's own snippets
]

[[label]]
name = "review"
skills = ["review"]                       # skill names, without a plugin prefix
snippets = ["review-implementation"]      # TabType snippet keys
jobs = ["review-"]                        # envoy job-name prefixes
count_commits = true                      # add "+N" commits since the latest event
color = "cyan"                            # the name's hue: blue, magenta, cyan, green or yellow
```

`snippets` is a TabType file or a list of them, in TOML. A file that does
not exist holds no snippets, so one configuration serves a machine that
lacks a project's checkout; a file that does not decode is an error. A
snippet is matched by the opening 80 characters its file holds today, and a
key worded differently in another file is recognised by either wording. Name
a project's file and list its keys under their labels together: a pasted
prompt is not read for the skills it names, so a key no label lists leaves
nothing among the steps.

A label with no `color` takes blue, magenta, cyan or green by its place in
the file; yellow is by choice only, since it does not carry text on a light
background. Labels may overlap: a step under two shows both names. A skill,
snippet or round that no label lists appears in `show --all`. An unknown key
is an error.

## Notes

Notes are kept in `$XDG_STATE_HOME/claude-steps/notes/<session id>.jsonl`
(default `~/.local/state`), one line per note, and can be edited by hand. They
belong to a session id: resuming keeps it, and so does `/cd`. `/clear` and a
forked session start a new id with no notes; a fork's timeline starts with
the history it copied.

## Development

```sh
make check     # tests with the race detector, vet, gofmt
make install   # build into ~/.local/bin
```

`docs/design.md` is the mental model for changing it; `CLAUDE.md` holds the
rules to keep.
