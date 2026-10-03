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
~/dev/app  feat/calendar
PR #7145 opened here  acme/app   1 compaction, last 2 hours ago

review    2 hours ago      1 commit since    review-r2 collected
verify    15 minutes ago   0 commits since   skill verify-local spikes
docs      ·

  3 hours ago      /review  codex full review
  3 hours ago        review-r1  collected
  3 hours ago      commit  the calendar walks days once (review r1)
  2 hours ago        review-r2  collected
  2 hours ago      compaction (manual)
  1 hour ago       commit  docs: the stories on the local rig
  1 hour ago       PR #7145 opened here  acme/app
  15 minutes ago   skill verify-local  spikes

notes
  1 hour ago   skip the docs pass, nothing user-facing changed
```

The view states dated facts. It does not say a check is finished or still
covers the code: "review, 2 hours ago, 1 commit since" is for you to judge.

## Commands

```text
claude-steps show [<pane>|<session>] [--json]   one session: its timeline and notes
claude-steps board [--json] [--ids]             every Claude pane in tmux, one row each
claude-steps note <pane>|<session> <text…>      append a note to a session
claude-steps check                              test the reader against recent transcripts
claude-steps import-notes <session id>          merge notes from another machine, read on stdin
```

`<pane>` is a tmux pane id such as `%12`; `show` defaults to the pane it runs
in. `<session>` is a session id or its first eight or more characters.
`claude-steps --help` has the rest.

`board` finds Claude panes through the tmux pane option `@claude_ctx_sid`.
Without something setting it, the board is empty and `show <session id>`
still works.

## Reading a view

- **`/review  args`:** a slash command you typed that loaded a skill.
- **`skill review  args`:** the model called the Skill tool; `failed to load`
  when the call returned an error.
- **`read skills/x/SKILL.md`:** the model read a skill's file, as it does
  when a prompt points at the path.
- **`pasted <key>`:** a prompt holding the opening of a TabType snippet.
- **`you: "…"`:** a prompt that named a labelled skill and ran nothing. Your
  words, not a run.
- **`review-r1  dispatched` / `collected`:** an `envoy run` and its later
  collect; `collected, envoy said partial` when envoy's status is not `ok`.
- **`commit  subject`:** a `git commit` in a call that returned no error.
- **`PR #12 opened here` / `linked`:** opened here only when `gh pr create`
  in this session returned its URL.
- **`compaction (manual)`**, **`note: …`.**

On the board a label's cell is the time of its latest event. `+2` counts the
commits made since that event started; `read` or `named` in front says the
latest event was only a file read or only a prompt; `·` says nothing matches.

A transcript that cannot be read says so (`no transcript`,
`transcript unreadable`, `3 lines could not be read`) and is never drawn as an
empty timeline. `the reader may have missed …` means Claude Code's transcript
format may have moved: run `claude-steps check`.

Not shown: commits made by a subagent, by `git merge`, `rebase` or
`cherry-pick`, or inside a script.

## Configuration

`~/.config/claude-steps/config.toml`, optional. The binary ships no labels;
with no file the board shows sessions, pull requests, compactions and notes.

```toml
projects_dir = "~/.claude/projects"            # default
snippets     = "~/.config/tabtype/config.toml" # default; no file, no snippet lines

[[label]]
name = "review"
skills = ["review"]                       # skill names, without a plugin prefix
snippets = ["review-implementation"]      # TabType snippet keys
jobs = ["review-"]                        # envoy job-name prefixes
count_commits = true                      # add "+N" commits since the latest event
```

A skill, snippet or round that no label lists still appears in the timeline.
An unknown key is an error.

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
