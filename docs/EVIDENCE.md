# Evidence

What the views showed for real sessions, one dated entry per pass. An entry
holds its predicates, its counts, what changed and what the next pass should
compare. It holds counts and short session ids only: this repository is
public, and no transcript's text belongs in it.

## 2026-10-04: the steps of sessions with many rounds

**Question.** Do the steps give the big picture of a session whose pull
request goes through many consult, review and docs passes? The pass opened on
the user's report that one review round read as two steps or more, so the
count of `review` lines said nothing about the count of rounds.

**Corpus.** Sessions in the obelisk index with `source = 'claude'`, started
2026-09-01 or later, 100 messages or more, and a project path like
`%planlab%` or `%worktrees-main-%`: 227 sessions, all read `ok`. Each was
rendered with `show`, `show --all` and `show --json` at `COLUMNS=150` before
and after the change, under the labels of that day (consult, review, verify,
docs, prompts). envoy named a job by a timestamp before 2026-09-14 and such a
round is under no label, so line counts are over the 117 sessions started on
or after that day, 87 of which hold a round.

### Findings

- **A round was two step lines, and three with the skill run before it.**
  In the 87 sessions, 296 labelled rounds and 166 `review` or `consult`
  skill runs took 739 step lines. One line per round takes 462, and 311 with
  the skill run on its round's line. 151 of the 166 runs were followed by a
  dispatch under the same label, a median of 1.9 minutes later (p90 3.4, one
  above an hour); 139 of the 296 rounds followed another round with no skill
  run between. Examples: `a9eafb70`, `b0b522b9`.
- **Most "no collect seen" rows were not rounds waiting.** 17 rows in 17
  sessions. 8 were a name dispatched again 0.2 to 0.9 minutes later and
  collected under the later dispatch; 2 were collected through a loop
  variable the reader did not name (`10f230af`).
- **A collect's job was read from text that does not hold it.** 9 rounds in
  7 sessions were named `$j`, `')` or the tail of a command substitution
  (`75e316e5`, `7265ba07`).
- **A mention quoted Claude Code's paste tag.** 26 of 164 mentions opened
  with `<pasted_content id="…">`, 26 of the 60 runes a mention shows.
- **Skills of a pull request's life that no label lists** (sessions of 227,
  then calls): `pl-loopy-onboarding` 134, 143; `pl-handle-code-review` 107,
  424; `pl-loopy-handoff-distill` 90, 102; `write-spec` 57, 61;
  `resolving-merge-conflicts` 33, 38; the `implement-spec` snippet in 12
  sessions.
- **A round named for a topic is under no label.** 16 dispatched rounds since
  2026-09-14 (`<topic>-review-r1`, `<topic>-consult-r1`): `jobs` matches a
  prefix.
- **A pull request is not a step.** 160 of the 227 sessions link one and 10
  link three or more. The header lists them; the steps do not say where in
  the order one was opened, and five on one line overflow 150 columns
  (`b0b522b9`).

### Changed

- **A round is one step,** dated at its dispatch, with the latest skill run
  before it and the dispatches replaced under its name (`render.outline`).
  The full history is as it was.
- **A dispatch replaced under its name is not "no collect seen"**
  (`Event.Redispatched`, `Record.Uncollected`).
- **A collect is named by envoy's `job:` lines when its text does not give
  the job,** and counted as a miss when neither does (`decode.go`).
- **A mention drops the paste tag.**

### Measured after

- **Step lines,** 87 sessions with a round: 1329 to 899 in total, median 15
  to 10, p90 24 to 16. The 25 with five rounds or more: median 22 to 14,
  the longest 37 to 21.
- **"no collect seen":** 17 rows to 7. The 7 hold no collect anywhere in
  their transcript.
- **Round names holding shell syntax:** 9 to 0. 3 sessions gained "the reader
  may have missed 1 × envoy round": a collect whose job neither the text nor
  the output names.

### Decided by the user

- **Labels for the skills above: yes.** The dotfiles package gained a `spec`
  label (`write-spec`, the `implement-spec` snippet), a `pr-review` label
  (`pl-handle-code-review`) and `pl-loopy-handoff-distill` under `docs`, in
  the order a pull request goes through them. In the trial they put the
  whole life of `a9eafb70`'s pull request in its steps. Over the 117
  sessions the median went from 8 step lines to 11 and the p90 from 16 to
  23; 67 sessions gained `pr-review` steps, 2 at the median and 13 at most.
- **A pull request as a step: no.** What is wanted is whether the work has
  one and its link, so the header prints each with its URL, newest first,
  on lines of their own when the header does not fit one.
- **`jobs` matching a name after a topic: not now.** 16 rounds; reopen if
  topic-named rounds grow.

### Limits

- Past sessions were rendered, and the popup's own test in the dotfiles passes
  on the installed binary; nobody was watched reading the popup, and the
  preview's height there was not measured. One cold reader, given the README,
  the help text and a view of nine rounds, counted the rounds and the skill
  runs right; its five wording defects were fixed in the README.
- A skill run is joined to its round by order alone. One join in 151 spans
  more than an hour.
- A prompt that pastes `/review …` seconds before the same slash command is
  two facts and still two lines (`a9eafb70`).

### The next pass

Compare sessions started after 2026-10-04 that hold three rounds or more.

- **Success:** the `review` and `consult` lines of a session count its
  rounds, and no "no collect seen" row names a round its transcript shows
  collected.
- **Revise the labels** if `pr-review` crowds the steps: it added up to 13
  lines to a session in the trial, where equal lines in a row are one.
- **Revise the join** if a round's line carries a skill run that led to
  something else, or if the collect's time is wanted among the steps.
- **Revise the miss** if `claude-steps check` fails on envoy rounds, or the
  `!` on a session for an unnamed collect turns out to be noise: it stood on
  3 of 227.
