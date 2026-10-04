# Working on claude-steps

A personal CLI that shows what has run in each Claude Code session, read from
its transcript. Read `docs/design.md` before changing behaviour; `README.md`
is what the user sees.

- **Nothing may reach a session.** No write under the projects directory, no
  keys sent to a pane, no network package, no process but `tmux list-panes`.
  `TestOnlyTheNotesAreWritten`, `TestNoNetworkInTheBinary` and
  `TestOneReaderAndOneReadOnlyProcess` hold this; a change that needs one of
  them loosened is a design change, not a fix.
- **Print facts, never verdicts.** No tick, "done", "passed" or "stale" in the
  tool's own words, and no colour that grades a date: a hue names a label or
  marks something unread. A label cell is a date; its grammar is pinned in
  `TestBoard`.
- **Transcript row shapes belong to `internal/record/decode.go` alone.** A
  rule added elsewhere is a second reader. After a Claude Code update, run
  `claude-steps check` before trusting a view.
- **Tests never read real transcripts or write real notes.** Build rows with
  `internal/fixture` and run under a temporary `HOME`; no private transcript
  is committed, and this repository is public.
- **Contract changes cross repositories.** `board --ids`, `show --all`, the
  colour and width environment, the notes file, `import-notes` and the
  configuration keys are used by `~/dotfiles` (`tmux-steps.sh`,
  `claude-tomini`, the `claude-steps` package). `docs/design.md` § Contracts
  other repositories depend on.
- Callers run `claude-steps` from PATH. Run `make check` before shipping and
  `make install` after: a source edit alone leaves the popup on the old
  binary.
- Library and CLI documentation questions: follow
  `~/.agents/skills/find-docs/SKILL.md`.

## Documentation

The live docs are `README.md` (using it), `docs/design.md` (changing it) and
this file. They follow `~/dotfiles/docs/documentation-standards.md`, with
paths written from the repository root. `docs/specs/` holds build records:
nothing live depends on one, and a shipped spec's decisions belong in
`docs/design.md`. `docs/EVIDENCE.md` logs each pass over real sessions, one
dated entry a pass, in counts and short session ids only: no transcript's
text goes into a public repository.
