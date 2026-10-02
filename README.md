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
verify    15 minutes ago   0 commits since   skill pl-loopy-verify local spikes
docs      ·

  3 hours ago      /review  codex full review
  3 hours ago        review-r1  collected
  3 hours ago      commit  the calendar walks days once (review r1)
  2 hours ago        review-r2  collected
  2 hours ago      compaction (manual)
  1 hour ago       commit  docs: the stories on the local rig
  1 hour ago       PR #7145 opened here  acme/app
  15 minutes ago   skill pl-loopy-verify  local spikes

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
```

`<pane>` is a tmux pane id such as `%12`; `show` defaults to the pane it runs
in. `<session>` is a session id or its first eight or more characters; a
prefix that matches two sessions is refused with both listed.

`board` finds Claude panes through the pane option `@claude_ctx_sid`, which the
dotfiles context chip sets. Without the chip the board is empty and
`show <session id>` still works.

`board --ids` starts every line with the pane id, a tab, the session id and a
tab (both empty on the header line), so a picker such as fzf can hide the two
fields and act on them.

`--json` prints RFC 3339 times and nothing relative.

## What a line means

| line | lifted from |
|---|---|
| `/review  args` | a slash command you typed, when a skill's expansion follows it |
| `skill review  args` | the model calling the Skill tool; `failed to load` when the call returned an error |
| `read skills/x/SKILL.md` | the model reading a skill's file, as it does when a prompt points at the path |
| `pasted <key>` | a prompt that holds the opening of a TabType snippet |
| `you: "…"` | a prompt that names a labelled skill and ran nothing; your words, not a run |
| `review-r1  dispatched` | `envoy run review-r1` in command position |
| `review-r1  collected` | a later `envoy collect review-r1`; `collected, envoy said partial` when envoy's status word is not `ok` |
| `commit  subject` | `git commit` in command position in a call that returned no error |
| `PR #12 opened here` | a pull request link, when `gh pr create` in this session returned its URL; `linked` otherwise |
| `compaction (manual)` | a compaction of the main conversation |
| `note: …` | your note |

On the board a label's cell is the time of its latest event. `+2` counts the
commits made since that event started (for a round, since its dispatch).
`read` and `named` in front of the time say the latest event was only a file
read or only a prompt.

What is not read: subagent transcripts, so a commit made by a subagent is not
listed; commits made by `git merge`, `rebase` or `cherry-pick`, or inside a
script; a commit whose call ended with `; true` after a failure, which counts.

A transcript that cannot be read is said to be so: `no transcript`,
`transcript unreadable`, or `3 lines could not be read`. Nothing unreadable is
drawn as an empty timeline.

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
An unknown key is an error, so a misspelt one cannot leave a column empty.

Notes are kept in `$XDG_STATE_HOME/claude-steps/notes/<session id>.jsonl`
(default `~/.local/state`), one `{"at": …, "text": …}` line per note. They are
plain text: edit or delete them by hand.

## Notes and session ids

Notes belong to a session id. Resuming keeps the id, so the notes stay. `/cd`
moves the transcript to another project directory and keeps the id. `/clear`
starts a new id with an empty timeline and no notes. A forked session also
has a new id and no notes, and its timeline starts with the history it copied.

## Format drift

Claude Code documents its transcript format as internal and free to change.
Every rule that reads it is in `internal/record/decode.go`. Each fact is
counted two ways as a transcript is read: by the reader's rule, and by a
second trace that should exist whenever the first does. When the second trace
sees a fact the rule missed, the session's view says
`the reader may have missed …`, and `claude-steps check` sums the counts over
the last week's transcripts and exits non-zero when more than one in ten is
missed.

## Development

```sh
make check     # tests with the race detector, vet, gofmt
make install   # build into ~/.local/bin
```

Tests build transcripts from `internal/fixture`, which copies the shape of real
rows with invented text, and run every command against a temporary home. tmux
is substituted at the one function that calls it.

| package | owns |
|---|---|
| `internal/record` | the session record: finding the transcript, decoding it, the label states |
| `internal/shell` | splitting a Bash call into the commands it runs |
| `internal/notes` | the notes store |
| `internal/panes` | the one tmux call |
| `internal/config` | paths, labels, snippets |
| `internal/render` | text and JSON |

The design, its premises and the numbered obligations the tests pin are in
`docs/specs/session-view.md`.
