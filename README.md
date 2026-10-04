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
~/dev/app  feat/calendar   PR #7145 opened here  acme/app   1 compaction, last 2 hours ago
────────────────────────────────────────────────────────────────────────
review   2 hours ago      1 commit since    review-r2 collected 1 hour ago
verify   15 minutes ago   0 commits since   skill verify-local spikes
docs     ·

no collect seen   ·
notes             1 hour ago   skip the docs pass

steps
  verify   15m   skill verify-local  spikes
  note     1h    skip the docs pass
                 1 commit
  review   1h    review-r2  collected
  review   2h    review-r2  dispatched
  review   3h    review-r1  collected
                 1 commit
  review   3h    review-r1  dispatched
  review   3h    /review  codex full review

11 rows in the full history (show --all)

$ claude-steps board
pane      session                       review  verify  docs      no collect  PR     note
work:2.1  The calendar walks days once  2h +1   15m +0  ·         ·           #7145  skip the docs pass
work:2.2  Split the importer            ·       ·       named 3h  1h ×2       ·
```

The view states dated facts. It does not say a check is finished or still
covers the code: "review, 2 hours ago, 1 commit since" is for you to judge.

## Commands

```text
claude-steps show [<pane>|<session>] [--all] [--json]   one session: its labels, notes and steps
claude-steps board [--json] [--ids]                     every Claude pane in tmux, one row each
claude-steps note <pane>|<session> <text…>              append a note to a session
claude-steps check                                      test the reader against recent transcripts
claude-steps import-notes <session id>                  merge notes from another machine, read on stdin
```

`<pane>` is a tmux pane id such as `%12`; `show` defaults to the pane it runs
in. `<session>` is a session id or its first eight or more characters; a
live pane's full session id works before the session has a transcript.
`claude-steps --help` has the rest.

`board` finds Claude panes through the tmux pane option `@claude_ctx_sid`.
Without something setting it, the board is empty and `show <session id>`
still works.

## Reading a view

A session view reads from the top, newest first:

- **The header:** the title, the session id and its pane; then the directory,
  the branch, the pull requests and the compactions. A transcript that could
  not be read in full says so on the next line.
- **The labels:** one row for each, always: when its latest event happened,
  the commits made since where the label counts them, and the event. `·`
  says nothing in the session is under the label.
- **`no collect seen`:** the rounds this session dispatched for which the
  transcript holds no collect, newest first, whether or not a label lists
  them. Such a round may be running, collected from another session, or
  given up on; the transcript cannot tell which.
- **`notes`:** your latest notes. `show --all` lists every one.
- **`steps`:** what ran under a label, and your notes at the time you wrote
  them. The commits between two steps are one count line. The last line says
  how many rows the full history holds.
- **`show --all`** prints that history in the steps' place: every line below,
  each with the labels it is under. With no label configured, `show` prints
  it too.

What a line says:

- **`/review  args`:** a slash command you typed that loaded a skill.
- **`skill review  args`:** the model called the Skill tool; `failed to load`
  when the call returned an error.
- **`read skills/x/SKILL.md`:** the model read a skill's file, as it does
  when a prompt points at the path.
- **`pasted <key>`:** a prompt holding the opening of a TabType snippet.
- **`you: "…"`:** a prompt that named a labelled skill and ran nothing. Your
  words, not a run.
- **`review-r1  dispatched` / `collected`:** an `envoy run` and its later
  collect, each at its own time; `collected, envoy said partial` when envoy's
  status is not `ok`.
- **`commit  subject`:** a `git commit` in a call that returned no error. A
  commit that may have been skipped (after `||`, inside an `if` or a loop,
  in the background) counts only when git printed its `[branch sha]` line.
- **`PR #12 opened here` / `linked`:** opened here only when `gh pr create`
  in this session returned its URL.
- **`compaction (manual)`.**

On the board a label's cell is the time of its latest event (`11m`, `2d`);
for a round that is the dispatch. `+2` counts the commits made since that
event started; `read` or `named` in front says the latest event was only a
file read or only a prompt; `·` says nothing matches. `no collect` holds the
time of the newest round with no collect seen, and `×2` when there are two.

A transcript that cannot be read says so (`no transcript`,
`transcript unreadable`, `3 lines could not be read`) and is never drawn as an
empty timeline. `the reader may have missed …` means Claude Code's transcript
format may have moved: run `claude-steps check`. On the board `!` before a
title marks such a session, and the words are in its note cell where there is
room.

Colour names and never grades. Each label's name has a hue; red marks an
error the transcript reports and what the reader could not read; a count of
commits is bold when there are any. Output is coloured on a terminal, or
through a pipe with `CLICOLOR_FORCE=1`, and never with `NO_COLOR` set.

With `COLUMNS` set, a view fits that width: an event's text is cut to one
line, and on the board the title and the note give way while the label cells
keep theirs. A note with no room is left to the session view.

Not shown: commits made by a subagent, by `git merge`, `rebase` or
`cherry-pick`, or inside a script.

## Configuration

`~/.config/claude-steps/config.toml`, optional. The binary ships no labels;
with no file the board shows sessions, rounds with no collect, pull requests
and notes, and `show` prints the whole timeline.

```toml
projects_dir = "~/.claude/projects"            # default
snippets     = "~/.config/tabtype/config.toml" # default; no file, no snippet lines

[[label]]
name = "review"
skills = ["review"]                       # skill names, without a plugin prefix
snippets = ["review-implementation"]      # TabType snippet keys
jobs = ["review-"]                        # envoy job-name prefixes
count_commits = true                      # add "+N" commits since the latest event
color = "cyan"                            # the name's hue: blue, magenta, cyan, green or yellow
```

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
