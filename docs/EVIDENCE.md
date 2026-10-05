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

## 2026-10-05: the reader before and after a code-quality pass

**Question.** A pass gave rounds one owner and changed how a collect and a
failed run are read. Does it print anything else for real sessions, and is
what it changes right? The cold voice of `consult-r1` had shown the faults on
fixtures; this pass asked how many real sessions held them.

**Corpus.** The 613 sessions whose transcript changed in the 30 days before
2026-10-05, on the laptop. Each was rendered with `show`, `show --all` and
`show --json` at `COLUMNS=150` by the binary built at `f8cd50b` and by the one
built at the pass's end. A pair that differed was rendered again, since two
runs can fall either side of a relative time. Both binaries ran `check` over
the 190 transcripts of the last 7 days.

### Findings

- **A fan-out's members were listed as rounds.** 2 sessions: a collect through
  a variable printed the set's block and each member's, and the members became
  rounds named `codex` and `codex-2` (`75e316e5`, `918c0183`).
- **A collect was joined to a dispatch made after it.** 1 session, 2 calls:
  `envoy collect x`, then `envoy run x`, in one call. The collect read as the
  later dispatch's, and the earlier dispatch as replaced (`4c5605d8`).
- **No session printed differently for the other faults the fixtures show:**
  a call's collects sharing one status, a failed run taking the count of the
  dispatches before it, a variable set in a subshell read outside it.
- **The pass's own first cut took a collect's block for a run's.** 1 session:
  a run and a collect in one call, each cut by `tail`, so the output held the
  collect's block alone (`502cbcb2`). This comparison found it.

### Changed

- **A collect belongs to the round its name read when its command ran,** and
  each command takes its own block of the output, a run's or a collect's
  (`internal/record/rounds.go`).
- **A fan-out's own block names the round.**
- **A run's error is read against envoy's job line,** and a round that was
  collected counts for its label whatever its run returned.

### Measured after

- **`show`:** 3 of the 613 sessions print differently, the 3 named above, and
  the same 3 under `show --all`.
- **`show --json`:** 14 sessions differ. They hold 513 events before and
  after; 10 events carry `dispatches`, and 1 carries `collect_failed` where it
  had `"outcome": "error"`.
- **`check`:** the same output from both binaries over the 190 transcripts,
  and no drift. envoy rounds: 201 by the reader's rule, 78 by the second
  trace, 4 missed.

### Decided by the user

- **A dispatch the transcript cannot prove: leave it.** A run on a branch
  decided at run time, and a run envoy refused in a call that returned no
  error, read as dispatched. 1 session of the 613 shows it: 2 "no collect
  seen" rows in `4c5605d8` are runs envoy refused, in a test of envoy itself.

### Limits

- The faults no session showed were fixed on fixtures alone.
- Sessions were rendered and compared; nobody read the views. The popup's own
  test in the dotfiles passes on the installed binary.
- The dispatches in the corpus that stand on a branch were not counted.

### The next pass

- **Reopen the decision above** if a "no collect seen" row names a run that
  did not happen, in a session that is not a test of envoy.
- **Revise the block rule** if envoy prints anything between a collect's job
  line and its status: that adjacency is how a collect's block is told from a
  run's.

## 2026-10-05: a skill loaded with `cat`

**Question.** A session had run its verification and its `verify` cell read
`named`, a prompt's words and no more (`ce72f9de`, on the mini). How does a
session load a labelled skill, and which loads does the reader not see?

**Corpus.** The transcripts changed in the 14 days before 2026-10-05: 379 on
the laptop and 103 on the mini, written below in that order. Commands were
read with `internal/shell` for the skills the labels of that day list. Each
session was rendered with `show --json` by the binary built at `4ba409b` and
by the one built at the pass's end.

### Findings

- **The model printed the skill's file with `cat` and called no tool for
  it.** In `ce72f9de` one prompt asked for a review and, in prose, for
  `pl-loopy-verify`. The model called the Skill tool for `review` and ran
  `cat` on the other skill's file, which the session's skill listing held.
- **`cat` is how a whole skill file is printed.** 78 and 21 `cat` commands
  name a labelled skill's file. None stands in a call that returned an
  error; 2 stand after `||`, the second place the file was looked for.
- **For `pl-loopy-verify` it is about one load in three.** By session,
  counted by a pattern over the command text: the Skill tool 9 and 9, the
  Read tool 4 and 2, `cat` 6 and 6, and `cat` alone in 6 and 5. No session
  typed it as a slash command. Over every labelled skill `cat` is the only
  load for 58 and 14 pairs of skill and session, `prompt-engineering` in 25
  and 7 of them.
- **A passage is shown more often than the file.** `grep` names a labelled
  skill's file 114 and 34 times and `sed -n` with a line range 65 and 20.
- **The output does not prove a `cat`.** The skill's `name:` line opens a
  line of the output for 88 of the 99. Of the other 11, 5 ran `cat -n`
  (`e0613ba6`, `4fc50d59`, `4aa4a757`), 3 had their output set aside by
  Claude Code with its start kept (`2eeff918`, `a0c6ac6e`, `626bc884`), and 3
  were piped into `head` or `sed` (`d057e681`, `75e316e5`).
- **The prompt that asked was a project-local snippet.** planlab's
  `loopy-review-verify`, from its `.tabtype.local.toml`, was pasted in 11
  and 9 sessions and `loopy-prompt-check` in 4 and 4; the global
  `review-verify` in none. The reader loads one snippets file, so each paste
  reads as a mention.

### Changed

- **A `cat` that prints a skill's file into the call's output is a read**
  (`skillsPrinted` in `internal/record/commands.go`), on the call's success
  and only when the command is not guarded.
- **`internal/shell` says when a pipe, a file or a substitution takes what a
  command prints** (`Command.Captured`).

### Measured after

- **`ce72f9de`:** `verify` reads `read`, with 2 commits since, and the read
  is a step.
- **Events:** 216 and 56 read events more, for labelled skills and others.
  No other event differs.
- **Label states:** 58 differ in 43 laptop sessions and 14 in 12 on the
  mini. What stood there before: a mention 31 and 6, nothing 9 and 4, a
  snippet 6 and 3, a skill run 9 and 0, an earlier read 3 and 1. By label on
  the laptop: `prompts` 27, `docs` 17, `verify` 5, `consult` 4, `review` 2,
  `pr-review` 2, `spec` 1; on the mini `prompts` 7, `verify` 6, `docs` 1.
- **A skill run behind a later read:** the 9 are under `docs`. In 7 a
  `/pl-loopy-handoff` run was followed by `cat` on
  `pl-loopy-handoff-distill`'s file, 0 to 93 minutes later, and the commit
  count runs from the read: lower in 5 (`1d1976e1`, `4179276c`, `8889c77f`,
  `bfe57e0d`, `c8b9f7df`). In 2 `update-docs` was printed in the minute of
  its own run.
- **An earlier read behind a later one:** 4 label states in 3 sessions, the
  commit count lower in 2 (`4fc50d59`, `4aa4a757`).
- **The sessions that pasted `loopy-review-verify`:** `verify` holds a skill
  run in 14 of the 20, a read in 3 and a mention in 3. Of those 3, 2 read
  the skill in passages with `sed -n` (`d057e681`, `cc8ba647`) and 1 holds no
  load after the paste (`a8e176f3`).
- **`check`:** no drift from the new binary on either machine.

### Decided by the user

- **A passage is not a read.** `sed`, `head` and `grep` on a skill's file
  stay out.
- **Project-local snippets: a design question for a later session.** Whether
  the reader should load a project's `.tabtype.local.toml`, from where, and
  which label a paste that asks for two steps stands under.

### Limits

- Sessions were rendered and compared. The 13 label states where a skill run
  or an earlier read stood were each looked at; the other 59 were counted.
- The per-skill session counts come from a pattern over the command text,
  the command counts from `internal/shell`.
- A snippet was matched by its wording of 2026-10-05, so pastes of an
  earlier wording are not in the counts.

### The next pass

Compare sessions started after 2026-10-05 whose prompt names a labelled
skill in prose.

- **Success:** a label reads `named` only where the transcript holds no load
  of the skill.
- **Revise the passage rule** if skills read from their first line with
  `sed -n` grow past the 2 of 20 above.
- **Reopen the read that follows a run** if a `docs` or `review` cell dated
  at a read hides the run the user wanted dated: the commit count is lower
  in 7 of the 482 sessions.

## 2026-10-05: a project's own snippets

**Question.** planlab starts its review, verification and prompt passes from
snippets in the project's own `.tabtype.local.toml`, which the reader did not
load, so each paste read as a mention. Should the reader load such a file,
from where, and what would a recognised paste change?

**Corpus.** Two populations, written below as the laptop then the mini.

- **The rendered window:** the transcripts last changed at or after
  2026-09-21 13:00 local time, 379 and 104, which is 480 sessions since 3
  are on both machines. The edge is fixed because a
  window cut 14 days before the run slides while the pass runs: 3 laptop
  sessions left it within the hour, one of them among the 3 below. Each
  transcript was rendered with `show --json` by the binary built at
  `6ffe9e0` under the labels of that day, and by the one built at the pass's
  end under the labels the pass set.
- **The indexed turns:** the user turns from 2026-09-21 in each machine's
  obelisk index, searched for a line of a snippet's text. A session moved
  between the machines can be in both. The search finds words, not a paste:
  one laptop session holds the line in the middle of another paste
  (`45e38b5a`), the reader finds no paste in it, and it is left out below.

### Findings

- **A session outlives its worktree.** Over the indexed turns 11 and 11
  sessions pasted `loopy-review-verify`, each in a planlab worktree. The
  working directory still stands for 3 and 2.
- **The paste arrives as text or as its own command.** By session: text
  alone in 8 and 7, a `/review` command alone in 1 and 2, both in 2 and 2
  (`8889c77f`, `a9eafb70`). Every arrival as text was wrapped in Claude
  Code's paste tag, and a command's arguments keep the snippet's line
  breaks: the number of lines does not decide which happens.
- **The reader never matched a command against a snippet.** The global
  `review-verify` is one line and arrived as a command in each of its 6 and
  3 pastes, which is why the entry above counts it in no session.
- **The two machines held the same files.** The copies of planlab's file
  were the same byte for byte, and so were the global files.
- **planlab's file had been rewritten that day.** Against the mini's copy of
  2026-09-27: `loopy-review-verify` and `loopy-prompt-check` open the same,
  the second edited past its 80th character; 2 keys are retired, pasted in
  no session of the indexed turns; `loopy-closeout` is reworded, its earlier
  wording pasted in 17 laptop sessions.
- **The binary of that day refuses a list in `snippets`,** and every command
  then fails with that message.

### Changed

- **`snippets` names a list of files,** read into one set for every session
  (`internal/config`).
- **A command row's name and arguments are matched** (`decoder.user`), and a
  paste typed as a command is a step only under a label that does not list
  the command's skill (`belongs`).
- **A cell dated at a paste says `pasted`** (`labelCell`).
- **Equal lines are one line only under the same labels** (`collapse`): a
  paste typed as its command and the same paste as text stay apart.
- **The board's columns close up to one space** when the label cells leave
  the title less than its floor (`Board`).
- **The labels, in the dotfiles:** planlab's file is named,
  `loopy-review-verify` stands under `review` and `verify`, and
  `loopy-prompt-check` under `prompts`.

### Measured after

Over the rendered window.

- **Paste events:** 14 and 13 of `loopy-review-verify` in 12 and 11
  sessions, 3 and 4 of them a command that loaded its skill; 4 and 5 of
  `loopy-prompt-check` in 4 and 4; 5 and 2 of `loopy-closeout` and 6 and 3
  of `review-verify`, each a command.
- **Mentions:** 15 and 14 fewer, the same prompts. No other event differs.
- **Label states:** 3 differ, each `verify` on the laptop, from a mention to
  a paste: `a8e176f3` with 10 commits since, `cc8ba647` with 14, `d057e681`
  with 4. None differs on the mini.
- **With the key under `verify` alone:** the same 3 and no other. **Under
  `review` alone:** those 3 go from a mention to nothing.
- **Cells that say `pasted`:** `spec` in 13 of 25 and 11 of 12 sessions that
  hold any event under it, each `implement-spec`; `prompts` in 6 of 64 and 1
  of 19, each `prompt-check`; `verify` in 3 of 25 and 0 of 21.
- **`check`:** no drift from the new binary over the laptop's 183
  transcripts of the last 7 days.

### Decided by the user

- **A paste dates its label,** with the commits since, and the cell says
  `pasted`.
- **`loopy-review-verify` stands under `review` and `verify`.**

### Limits

- Sessions were rendered and compared. Two views were read after the change
  (`a8e176f3`, `8889c77f`); the rest were counted.
- A command row's shape was read in three laptop transcripts.
- The 5 and 2 `loopy-closeout` rows are commands from before the snippet had
  those words (`~/dotfiles/tabtype/EVIDENCE.md`): typed by hand, and read as
  pasted.
- On the mini the new binary ran from a scratch directory, and `check` was
  not run there. The popup's own test in the dotfiles was not run.
- The word costs a column 7 columns. The live boards need 102 columns on the
  laptop, with no paste cell, and 117 on the mini, with `pasted` under
  `spec`; the popup gives about 121 and 151. No board was looked at with the
  columns closed up.
- The indexed turns and the rendered window hold the same pasting sessions
  but for `cc8ba647`, whose paste is older than the indexed turns. A
  directory that stands on 2026-10-05 says nothing of the day its session
  ran.

### The next pass

Compare sessions started after 2026-10-05 that paste one of planlab's
snippets.

- **Success:** a `verify` cell reads `named` only where the prompt was typed
  by hand.
- **Revise the word in the cell** if `pasted` under `spec` costs the title
  its room at the popup's width.
- **Reopen the source** if a second project keeps snippets of its own at a
  path that differs between the machines.
- **Look at the two copies** if a session moved with `claude-tomini` shows a
  `you: "…"` line where the laptop showed a paste: the copies have parted.
