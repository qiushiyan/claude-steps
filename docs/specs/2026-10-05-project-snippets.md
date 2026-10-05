# claude-steps: a project's own TabType snippets, read from the files the configuration names

Status: built (2026-10-05). The reader, its tests and the installed binary are on the laptop; the dotfiles carry the `snippets` line and the labels. Qiushi settled both calls in § Delivery that day.

## Summary

Current: `claude-steps` matches a prompt against the snippets of one file, the global `~/.config/tabtype/config.toml`, and only when the prompt is typed as text.
Current: planlab keeps the snippets that start its review, verification and prompt passes in `~/dev/planlab/main/.tabtype.local.toml`, a gitignored file with one copy per machine.
Failure: a paste from that file reads as a `you: "…"` line, a mention, and a mention never carries a commit count.
Failure: in 3 of the 23 sessions that pasted planlab's review snippet, the reader saw no load of the verification skill after it, so the `verify` cell reads `named` with no count.

Goal: a paste of a project's snippet is a dated `pasted <key>` line under every label that lists its key, with the commits made since.
Change: the `snippets` configuration key takes a list of files, and a prompt is matched against the snippets of all of them.
Change: a paste that arrived as a slash command is recognised from the command's name and arguments.
Change: a label's board cell says `pasted` when its latest event is a paste.

Boundary: the files are named in the configuration. Nothing is looked for from a session's working directory.
Boundary: TOML files only, matched by the opening 80 characters of the wording a file holds today, as the global file already is.
Risk: the binary before this change refuses a list in `snippets`, so the dotfiles change must reach a machine with the new binary or after it.
Decided: a paste dates its label and its cell says `pasted`; planlab's two-step snippet stands under `review` and `verify`.

Where: § Behaviour describes the six situations; § Design carries the rules, the shapes rejected and the measurements; § Verification numbers the obligations and names the test that pins each; § Delivery holds the order across repositories and the two calls Qiushi settled.

## Intent

### Goals

- A prompt that holds the opening of a snippet in any named file is a `pasted <key>` event, whether it arrived as text or as a slash command.
- A label that lists the key shows that paste as its dated event, with the commits made since where the label counts them, until a later event under the label takes its place.
- The board does not show a paste as if a skill had run: the cell says `pasted`.
- One configuration serves the laptop and the mini: a named file a machine lacks holds no snippets there, and nothing fails.

### Non-goals

- **Finding a project's file from the session's working directory.** § Design — Rejected shapes gives the measurement that rules it out.
- **Reading `.tabtype.local.json`.** TabType reads the JSON file when no TOML file stands beside it. Every snippet file on both machines is TOML on 2026-10-05, and a second decoder of TabType's schema would have no file to read. A named `.json` path fails as any file that does not decode does, with its path.
- **Reproducing TabType's precedence.** Inside a project TabType offers the project's wording of a key and elsewhere the global one. The reader cannot know which a session was offered, so it recognises every wording of a key in every session. That is a superset: it can add no key the prompt's words do not hold.
- **Recognising a wording no named file holds.** A snippet is matched by the opening it has today. § Design — Premises says what that cost in the measured window and how a retired wording is kept without new code.
- **Reading a prompt recognised as a paste for skill names as well.** § Design — Rejected shapes.

### What this is not

`pasted <key>` says the prompt holds the opening of the snippet `<key>`. It does not say TabType expanded it: a prompt typed by hand with the same opening reads the same. It does not say the step ran: a paste is a request, and the skill's run or read is a separate event when the transcript holds one.

## Tenets

- **A request is shown as a request, over a cell that reads like its neighbours.** A cell dated at a paste says `pasted`, as a cell dated at a file read says `read`. A bare date under `verify` reads as "the verification ran then". Held by: the cell grammar (§ Design — API) and obligation 5.
- **A transcript reads the same whatever has since happened to the directory it ran in, over reading each session's surroundings.** A session view is opened long after the work merged. Held by: `Load` builds one set from the configuration, and no rule reads a session's directory to find a file (§ Design — Structure).
- **One prompt, one line under a label.** A paste that is also its own command's run does not stand twice under the label that has the run. Held by: `belongs` in `internal/record/labels.go` and obligation 4.

## Behaviour

### A snippet from a project's file is pasted and arrives as text

Claude Code wraps a pasted block of several lines in `<pasted_content>`, so a snippet that opens with `/review` and runs over several lines arrives as text, and the model calls the Skill tool itself.

Today: the steps hold `you: "/review codex full review. While you…"` under `review` and `verify`. Each of those labels reads `named` on the board unless a run, a read or a round follows.

After: the steps hold `pasted loopy-review-verify` under every label that lists the key. A label with no later event shows the paste's time and the commits since, and its board cell reads `pasted 2w +10`.

Mechanism: `decoder.prompt` in `internal/record/decode.go`, unchanged, now matches against the snippets of every named file (§ Design — Wiring).

If it recurs: every paste is a line of its own. Equal lines in a row collapse to one with a count, as they do today.

### The same snippet arrives as its slash command

When the paste is not wrapped, Claude Code runs the `/review` it opens with, and the rest of the snippet is the command's arguments.

Today: the steps hold `/review  codex full review. While you are waiting, run…` under `review`. Nothing from the prompt stands under `verify`.

After: the same `/review` line under `review`, and `pasted loopy-review-verify` at the same time under `verify`. Under `review` the paste is not a second line: the run is that prompt's line there.

Mechanism: the command row's name and arguments are matched as one prompt, and the paste carries the skill its command ran (§ Design — API).

### The model loads the skill after the paste, or the paste comes after the load

Today and after, a label's state is its latest event that did not fail.

After: a skill run or a read that follows the paste becomes the label's event, as it is today. A paste that follows a run, a second request in a session that already holds the skill, becomes the label's event, and the commit count runs from that paste. The cell then reads `pasted`, and the earlier run is still a step.

### A named file is not on this machine, or does not decode

A file that does not exist holds no snippets. A paste from it then reads as it did before this change: a `you: "…"` line where the prompt names a labelled skill.

A file that exists and does not decode is an error that names the file, and no command runs until it is fixed. That is what the global file already does.

### A key that no label lists

The paste is a row of the full history (`show --all`) and not a step. The prompt is not read for skill names, so a label whose skill the snippet names shows nothing of it. Naming a file therefore means listing its keys under the labels they start (§ Delivery).

### One key in two files

A key that two files word differently is recognised by either wording, and both are `pasted <key>`. A prompt that holds both wordings is one paste.

## Design

The change extends the existing snippet rule. `config.Snippets` stays one set for every session, and nothing in `internal/record/load.go` changes.

### Structure

- **Which files hold snippets, and what a snippet is reduced to.** Owner: `internal/config` (`Load`, `loadSnippets`, `Snippets.Pasted`). Protects: every session is matched against the same snippets, whatever directory it ran in. Held by: the set is built once in `Load` from the configuration alone (obligations 1 and 6).
- **What the words of a prompt are when it is a slash command.** Owner: `internal/record/decode.go` (`decoder.user`, `slashCommand`), the one place that knows a command row's shape. Protects: a paste is recognised however Claude Code recorded it. Held by: obligation 3.
- **Which labels a paste is under.** Owner: `belongs` in `internal/record/labels.go`. Protects: a label's row and its steps agree, and one prompt is one line under a label. Held by: `Summarise` and `LabelsOf` both read `belongs` (obligation 4).
- **How a cell reads.** Owner: `labelCell` in `internal/render/board.go`. Protects: a cell is a date, with the kind of its event said when that is not a run. Held by: obligation 5.

### API

The configuration key, which the dotfiles `claude-steps` package sets:

```toml
snippets = ["~/.config/tabtype/config.toml", "~/dev/planlab/main/.tabtype.local.toml"]
```

- A string still names one file. With no key the list is the global file alone.
- A list replaces the default, so the global file is named again.
- `~/` is the home of the machine that reads, so one line serves `/Users/qiushi` and `/Users/qiushiyan`.
- Files are read in the order named. An entry with the same key and the same opening as an earlier one is dropped; the same key with another opening is kept beside it.

A snippet event gains one field, `command` in `show --json`: the skill its own slash command ran. It is empty for a paste that arrived as text and for a command that no expansion followed.

What a prompt holding a snippet's opening yields:

| the prompt arrives as | events | the paste is a step under a label that lists its key |
| --- | --- | --- |
| text, wrapped or bare | the paste | always |
| a slash command its expansion follows | the paste, with `command`, then the skill run | unless the label lists the command's skill |
| a slash command no expansion follows | the paste | always |

The whole grammar of a label's cell:

```text
·   <time>   <time> +N   read <time>   read <time> +N   pasted <time>   pasted <time> +N   named <time>
```

### Wiring

- Today: `Load` reads the one path in `snippets` (`internal/config/config.go`). After: it reads each path in the list and joins what they hold.
- Today: a command row sets `decoder.slash` and returns before `decoder.prompt`, so its words are never matched (`decoder.user`). After: the row's `/name arguments` is matched when the row is read, and the keys wait on the command as its name does. They are emitted where the command is settled: with the skill run when the expansion follows, in `unexpanded` otherwise, where a paste takes the place of the mention.
- Today and after: a prompt recognised as a paste is not read for skill names (`decoder.prompt`).
- Today: `belongs` puts a paste under every label that lists its key. After: except a label that lists the skill the paste's own command ran.
- Today: a paste and a skill run both draw a bare date in `labelCell`. After: a paste draws `pasted` in front.

### Rejected shapes

- **Find the file from the session's working directory**, the project root's file and, for a linked worktree, the primary checkout's. It optimises for no path in the configuration. It loses because a session outlives its worktree: of the 23 sessions that pasted planlab's review snippet, the working directory still exists for 5 on 2026-10-05 (§ Design — Premises), so 18 would read as a paste while the work was open and as a mention after the merge. It would also keep a second copy of two TabType rules, which directory is a project root and how a worktree names its primary checkout, and `config.Snippets` would become a set per session. The path it saves is one line in a file that must be edited anyway, since a label has to list the key.
- **Keep the opening to match in the label**, beside the key. It optimises for no dependence on any snippet file. It loses because the wording then has two homes, and the label's copy goes stale the day the snippet is reworded.
- **Read a pasted prompt for skill names too**, so a label that does not list the key still shows `named`. It loses because every paste would be two rows of the full history, in sessions this change does not otherwise touch.
- **Leave a slash command's words unmatched**, as today. It loses because the paste is then seen only while Claude Code wraps it: 7 of the 27 pastes of planlab's review snippet in the window arrived as a command that loaded its skill, and a snippet rewritten onto one line would arrive that way every time.
- **A bare date for a paste**, as a paste has today. The first call in § Delivery weighs it.

### Premises

The measurements below are of 2026-10-05, on the laptop then the mini. Two populations are used and each count names its own.

- **The rendered window:** every transcript last changed at or after 2026-09-21 13:00 local time, 379 and 104, rendered with `show --json` by the binary built at `6ffe9e0` under the labels of that day, and by this change's binary under the labels of § Delivery.
- **The indexed turns:** user turns from 2026-09-21 in the session index of each machine, searched for a line of the snippet's text. A session moved between the machines can be in both.

**Decision: name the files in `snippets`.** Settled.
Basis, measured over the indexed turns: 12 and 11 sessions pasted `loopy-review-verify`, every one in a planlab worktree. Their working directories still exist for 3 and 2.
Basis, from source: `mini-sync` in the dotfiles carries the `claude-steps` package to the mini, and planlab's primary checkout is at `~/dev/planlab/main` on both machines.
Does not establish: how long a worktree lasts after its session ends.

**Decision: a file that does not exist holds no snippets.** Settled.
Basis, from source: `loadSnippets` already skips a missing file, and the README says so for the default path. A machine without a project's checkout must still run.
Limit: a misspelt path is not reported. Its pastes read as `you: "…"` lines, which is how every project paste reads today.

**Decision: match a command row's name and arguments.** Settled.
Basis, measured over the rendered window: 14 and 13 pastes of `loopy-review-verify`, of which 3 and 4 arrived as a slash command that loaded its skill. By session, over the indexed turns: text alone in 9 and 7, a command alone in 1 and 2, both in 2 and 2. Every arrival as text was wrapped.
Basis, measured: the global `review-verify` is one line and arrived as a command in each of its 6 and 3 pastes. `docs/EVIDENCE.md` counts it as pasted in no session, because the reader never matched a command.
Limit: a command typed under a plugin's name (`/plugin:review`) is not matched to a snippet that opens with `/review`.

**Decision: a paste typed as a command is not a step under a label that lists the command's skill.** Settled.
Basis, measured over the rendered window: `review` lists the key `review-verify` and the skill `review`. With the rule the 6 and 3 pastes of `review-verify` are rows of the full history and add no step. Without it, each would add a line under `review` beside its own `/review` run.

**Decision: every wording of a key, in every session.** Settled.
Basis, measured: no key stands in both files today, and no local opening equals a global one. The dotfiles naming rule gives a project's keys the project's prefix (`~/dotfiles/tabtype/CLAUDE.md`).
Limit: two keys that share an opening are both pasted by one prompt, as they are within one file today.

**Decision: the reading machine's copy.** Settled.
Basis, measured: the two copies of `~/dev/planlab/main/.tabtype.local.toml` are byte for byte the same on 2026-10-05, and so are the two global files.
Basis, from source: `claude-tomini` rewrites a moved session's `cwd` fields and nothing else, so a moved session is matched against the mini's copy.
Limit: nothing keeps the two copies the same. A paste of a wording the reading machine's copy lacks reads as a `you: "…"` line.

**Decision: the current wording's opening 80 characters, as today.** Settled.
Basis, measured: planlab's file was rewritten on 2026-10-05, and the mini keeps the copy of 2026-09-27. `loopy-review-verify` and `loopy-prompt-check` open the same in both; the second was edited past its 80th character. Two keys were retired, and neither was pasted in any session over the indexed turns. `loopy-closeout` was reworded, and no label lists it: the 17 laptop sessions that pasted its earlier wording read as they do today, a `you: "…"` line and the load that followed.
Fallback, if a retired wording must stay recognised: a file that holds it under the same key can be named in `snippets`. That needs no code.
Limit: a prompt that holds a snippet's opening reads as `pasted` however it was entered. `loopy-closeout` is 45 characters, the words Qiushi also types by hand, and a command typed that way reads `pasted loopy-closeout` in the full history.

**What the change moves, measured over the rendered window.**

- **Events:** 14 and 13 pastes of `loopy-review-verify` in 12 and 11 sessions, 4 and 5 of `loopy-prompt-check` in 4 and 4, 5 and 2 of `loopy-closeout`, 6 and 3 of `review-verify`. 15 and 14 mention events are no longer shown: the same prompts, now pastes. No other event differs.
- **Label states:** 3 differ, all `verify` on the laptop, from a mention to a paste: `a8e176f3` with 10 commits since, `cc8ba647` with 14, `d057e681` with 4. None differs on the mini. `docs/EVIDENCE.md` says what those three hold after the paste: two read the skill in passages, one holds no load.
- **With the key under `verify` alone:** the same 3 states and no other.
- **With the key under `review` alone:** those 3 `verify` states go from a mention to nothing.
- **Cells that gain the word `pasted`:** `spec` in 13 of 25 and 11 of 12 sessions that have any event under it, all `implement-spec`; `prompts` in 6 of 64 and 1 of 19, all `prompt-check`; `verify` in 3 of 25 and 0 of 21.

Does not establish: how the views read for sessions started after 2026-10-05, when `loopy-handle-review` and the reworded `loopy-closeout` came into use.

## Verification

The build reports each obligation below by number: pinned, a test that goes red when the behaviour is removed; nominal, a test that exists but would stay green; or skipped, with the reason.

Every test builds its rows with `internal/fixture` under a temporary `HOME`. The snippet texts in them are invented; none is a real snippet's wording.

1. **Every named file is read, and a missing one holds nothing.** Observe: `config.Load` on a configuration that lists three files, one of them absent. Pinned by `TestSnippetFilesAreNamed` in `internal/config/config_test.go`.
2. **A key worded twice is one key; an entry repeated is one entry; `snippets` is a path or a list of paths.** Observe: `Snippets.Pasted` on prompts holding either wording and both, and `Load` on a number, on a list holding one, and on a named file that does not decode. Pinned by `TestSnippetFilesAreNamed` and `TestSnippetsWantsPaths`.
3. **A paste is recognised as text, as a command that loaded its skill, and as a command that loaded nothing.** Observe: a record loaded from fixture rows of the three shapes. Pinned by `TestAPasteArrivesAsAPromptOrAsItsCommand` in `internal/record/record_test.go`.
4. **A paste typed as a command is not under a label that lists the command's skill, and is under any other that lists its key.** Observe: `LabelsOf`. Pinned by the same test and by `TestAnEventCanBeUnderSeveralLabels`.
5. **A paste dates its label with the commits since, the cell says `pasted`, and a later load takes its place.** Observe: `Summarise`, and the board and the session view from a temporary home. Pinned by `TestAPasteDatesItsLabelUntilALoadFollows`, `TestAPasteFromAProjectsFile` in `cmd/claude-steps/board_test.go`, and the cell grammar in `TestBoard`.
6. **Without the file, the same transcript reads as before.** Observe: the board and the session view after the named file is removed. Pinned by `TestAPasteFromAProjectsFile`.
7. **The stands hold unloosened.** `TestOnlyTheNotesAreWritten`, `TestNoNetworkInTheBinary` and `TestOneReaderAndOneReadOnlyProcess` are unchanged and pass.

Limit: the fixtures prove the rule against the row shapes they build. That a real command row carries the snippet's words in `<command-args>` was read from three laptop transcripts on 2026-10-05 and is what the rendered window above measures.

## Delivery

**Repository boundary.** Two repositories change. This repository carries the reader, the tests and this spec in one pull request. The dotfiles carry the `snippets` line and the label lines in `~/dotfiles/claude-steps/.config/claude-steps/config.toml`.

**Order.** The new binary reaches a machine before the dotfiles change, or with it: the binary built at `6ffe9e0` refuses a list in `snippets` and every command then fails with that message. On the laptop that is `make install`, then the dotfiles edit. `mini-sync` copies the dotfiles tree and then the binary in one run, so the mini's commands can fail for the seconds between the two.

**The dotfiles change as built:**

```toml
snippets = ["~/.config/tabtype/config.toml", "~/dev/planlab/main/.tabtype.local.toml"]
```

`loopy-review-verify` is listed under `review` and `verify`, and `loopy-prompt-check` under `prompts`, beside the global `prompt-check` it stands in for. `loopy-handle-review` and `loopy-closeout` are listed nowhere: each is its own command's run under `pr-review` and `docs`.

**The live docs.** `README.md` and `docs/design.md` describe the reader as built, and `docs/EVIDENCE.md` holds the pass's entry. In the dotfiles, `~/dotfiles/tabtype/DESIGN.md` says how the board reads a snippet.

**Decided by Qiushi on 2026-10-05: a paste dates its label, and its cell says `pasted`.**

Choice: (A) the paste is the label's dated event with the commits since, and the cell says `pasted`; (B) the same, with a bare date; (C) a paste never dates `verify`.
Consequence of A, as built: the three `verify` cells read `pasted 2w +10` in place of `named 2w`. 31 cells that read as a bare date gain the word, 24 of them `implement-spec` under `spec`, where the paste is the whole step.
Consequence of B: those three cells would read `2w +10`, the same as a session whose verification skill ran.
Consequence of C: the file is not named at all. Naming it without the key under `verify` takes `named` away from those three cells (§ Design — Premises).
Reason for A: the paste is the one trace of the request that does not depend on how the model then loads the skill, and the pass before this one began with a load the reader did not count. A snippet was the whole step when the cell's grammar was written; most are now the sentence that starts a skill, so the cell says which it is.

**Decided by Qiushi on 2026-10-05: `loopy-review-verify` stands under `review` and `verify`.**

Choice: `review` and `verify`; or `verify` alone.
Consequence: the label states are the same either way over the rendered window. Under both, the paste's line carries both names, as the `you: "…"` line did, and a review that never started after a paste leaves `review` dated at the paste. Under `verify` alone the line carries one name and such a session shows nothing under `review`.
Reason for both: the snippet asks for both steps in so many words, and labels are not exclusive.
