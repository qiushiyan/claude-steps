# claude-steps: a dated record of each Claude Code session, read from its transcript

Status: built (2026-10-02). The command is in this repository and installed on the laptop; the dotfiles carry the `prefix S` popup and the labels. Still open in § Delivery: the first run on the mini, and removing the `steps` mod.

## Summary

Current: Qiushi runs several long Claude Code sessions at once in tmux panes, on a laptop and on an office Mac mini.
Current: to learn whether a review, a docs pass or a verification has run in a session, he asks the session.
Failure: asking costs a turn of the working session, and in one recorded case a "no" made the model start the work unasked.
Failure: a yes or no does not say when the step ran or what was committed since, which is often what he needs.

Goal: he presses one tmux key and reads, for every Claude session on the machine, what ran, when, and what was committed since.
Goal: he keeps free-form notes per session and sees them whenever he opens the view.
Change: a Go command, `claude-steps`, reads tmux and the session's transcript file and prints a board and a per-session timeline.
Change: the `steps` mod in the dotfiles (band, `/steps`, `/did`) is removed once the command is wired in.

Boundary: nothing the tool does reaches the model of a working session; it never writes to a session, and it makes no model call.
Boundary: the view states dated facts. It never says a check is done, passed or still valid.
Boundary: Claude Code sessions only, and each machine shows its own sessions.
Risk: Claude Code documents the transcript format as internal and free to change. The reader counts every fact two ways and says so on the view when the counts part.
Open: the `steps` mod is removed only after the key has shown a real session on both machines.

Where: § Behaviour describes what he sees; § Design carries the reader's rules, the premises and their evidence; § Verification numbers the obligations and names the test that pins each; § Delivery holds the repository boundary.

## Intent

### Goals

- For one session, show a timeline of dated events lifted from its transcript: skills run and skill files read, pasted prompt snippets, review and consult rounds and their collects, commits with their subject, pull requests, compactions, and his own notes.
- For all live sessions on the machine, show a board: one row per Claude pane, one column per configured label, plus pull requests, compaction count and the latest note.
- Let him append a note to a session from the view or from a shell.
- Report a transcript that is missing or cannot be read as exactly that.
- Run identically on the laptop and the mini, with nothing running between invocations.

### Non-goals

- **Telling the model anything.** Reminders that change what a session's model does belong in skills. This tool has no path to the session.
- **Verdicts.** No tick, no "done", no "stale". An invocation in a transcript does not prove a check finished or still applies, and the transcript is the only source.
- **Interrupting him.** Nothing is drawn until he presses the key. No pane-border field and no floating note.
- **Answering content questions** such as "did we add tests". The timeline answers a few by accident (commit subjects); nothing is built for them.
- **Codex sessions.**
- **A cross-machine view.** The laptop does not list the mini's sessions.
- **Editing or deleting notes through the command.** The notes file is plain text and can be edited by hand.
- **Reading subagent transcripts.** § Design — Premises records the limit.

### What this is not

The dotfiles `steps` mod recorded the same signals from inside the session and showed ticks. `claude-steps` replaces it and shares no code or state with it. "Check" in earlier discussions meant a group of skills with a state; this spec uses **label** for a named board column and gives it no state.

## Tenets

- **The tool has no handle on a session, over a convenient one.** It opens transcripts read-only and writes only inside its own state directory, because any write path into a session is a way to change what the model reads. Held by: obligations 14 and 15.
- **Facts over verdicts.** The view says what happened and when; he judges. A verdict computed from an invocation is wrong whenever the invocation failed, was interrupted, or predates the latest commit, and he would trust it. Held by: the event model carries no state for a label (§ Design — API) and obligation 5.
- **Unknown is shown as unknown, over a clean screen.** A line that cannot be decoded, a file that cannot be read and a fact the reader's rule may have missed are reported, because a silent skip reads as "nothing ran". Held by: the read status and the signals on every record (§ Design — API) and obligations 9, 10 and 19.
- **One reader.** Every view and the `check` command get a session's record from the same loader, so two views cannot disagree about a session. Held by: § Design — Structure and obligation 13.

## Behaviour

### He wants to know whether the last review covers the code

Before: he typed "did we run a codex review this session". The session answered from its own memory and spent a turn.

Now: `claude-steps show` in that pane (the tmux key, once wired) prints the session. Its label lines and the end of its timeline read, for example:

```text
review    2 hours ago      1 commit since    review-r2 collected

  3 hours ago      /review  codex full review
  3 hours ago        review-r1  collected
  3 hours ago      commit  the calendar walks days once (review r1)
  2 hours ago        review-r2  collected
  1 hour ago       commit  docs: the stories on the local rig
  1 hour ago       PR #7145 opened here  acme/app
```

"1 commit since" counts the commits made after `review-r2` was dispatched, because a reviewer reads the code as it stood at the dispatch. He decides himself whether that commit matters. The session received nothing.

### He looks across sessions

The board has one row per Claude pane on this machine, in tmux's order. Each label column holds one of these and nothing else:

| cell | meaning |
|---|---|
| `·` | no event in the session matches the label |
| `3 hours ago` | when the label's latest event happened |
| `3 hours ago +1` | the same, with the commits made since, where the label counts them |
| `read 3 hours ago` | the latest event is the model reading one of the label's skill files |
| `named 20 minutes ago` | only a prompt named one of the label's skills |

The row ends with the pull request numbers, the compaction count, and the start of the latest note, with the number of notes when there is more than one.

### He writes a note

`claude-steps note <pane or session> <text>` appends the note with the current time. Notes stay with the session id: he sees them again whenever he opens the view on that session, in any pane, after a resume. In the session view they appear in the timeline at their time and again in a `notes` section at the end.

The command refuses an empty note, a pane with no Claude session, and a session id this machine holds neither a transcript nor notes for. If the write fails it prints the reason and exits non-zero.

### A step was only named in a prompt

Most references to some skills are by name ("run pl-loopy-verify with local spikes") or by path ("follow …/skills/prototype/SKILL.md"), and the transcript then holds no skill load.

- A prompt that names a labelled skill appears as its own line with his opening words: `you: "run pl-loopy-verify with local spikes to re-prove…"`. The view does not claim the skill ran.
- When the model then reads the skill's file, that is a line of its own: `read skills/prototype/SKILL.md`.
- A slash command for a labelled skill with no expansion after it is shown the same way as a prompt: `you: "/review codex"`.

### A round failed, or was never collected

A round line shows what the transcript holds:

| line | the transcript holds |
|---|---|
| `review-r1  dispatched` | an `envoy run`, and no collect |
| `review-r1  collected` | a collect whose status word is `ok`, or that printed no status |
| `review-r1  collected, envoy said partial` | a collect with any other status word, in envoy's own word |
| `review-r1  collect returned an error` | a collect call that failed |
| `review-r1  run returned an error` | an `envoy run` call that failed |

Envoy's `ok` means the job returned a result. It is not printed, because beside a review it would read as "passed".

### The transcript is missing, unreadable, or partly unreadable

A session whose transcript file is not found shows `no transcript` on the board, and its session view shows that line and the notes. A file in which no line decodes as a row shows `transcript unreadable` with its path, and the notes. A file with some undecodable lines shows its timeline and `12 lines could not be read`. A file that was read and holds no event shows `no events in the transcript`. The first two are never drawn as an empty timeline.

When a second trace shows a fact the reader's rule missed, the session view and the board row say `the reader may have missed 1 × pull request (claude-steps check)`. He does not have to run anything to learn that the format moved.

`claude-steps check` reads the transcripts modified in the last seven days and prints each fact's two counts and the sessions where they part. It exits non-zero when a transcript is unreadable, when no transcript holds a conversation row, or when more than one in ten of a fact's second traces has no match.

### The session moved or changed identity

- **Resume** keeps the session id; the timeline and notes continue.
- **`/cd`** moves the transcript file to another project directory; the tool finds it by session id wherever it is.
- **`/clear`** starts a new session id with an empty timeline and no notes.
- **A fork** starts a new session id with no notes. Its timeline starts with the history Claude Code copied into it, dated as it happened. `--help` says both.
- **A move to the mini** with `claude-tomini` carries the transcript; the dotfiles change in § Delivery makes it carry the notes file too.

### Outside tmux, or a pane without Claude

`claude-steps show <session id>` works without tmux. `claude-steps board` with no tmux server prints `no tmux server` and exits non-zero; with a server and no Claude pane it prints that on stderr and exits zero. A pane with no Claude session is not on the board, and `show` on it says so and exits non-zero. `show` on a session whose transcript is missing or unreadable exits zero: the state is the answer.

## Design

### Structure

- **The session record.** Owner: `internal/record`. Given a session id it locates the transcript, decodes it into events, loads the notes, and returns a record with a read status. Protects: every caller sees the same events for a session, and a decoding failure is counted, never dropped. Held by: it is the only package that knows where transcripts live (obligation 13).
- **Transcript knowledge.** Owner: `internal/record/decode.go`. Every fact about row shapes lives there, so a format change is repaired in one file. Protects: rows are decoded structurally as JSON; no rule depends on key order or whitespace. Held by: obligation 9.
- **Command position.** Owner: `internal/shell`. It splits the text of a Bash call into the simple commands it runs, so quoted text, here-documents and commit messages never count as commands. Held by: obligations 4 and 6.
- **Label states.** Owner: `internal/record/labels.go`, so the board and the session view cannot compute them differently.
- **Notes.** Owner: `internal/notes`, a store keyed by full session id. Protects: an append never loses another append, and notes are readable when the transcript is gone. Held by: obligations 11 and 12.
- **Live panes.** Owner: `internal/panes`, the only package that starts a process: `tmux list-panes`. Held by: obligation 14.
- **Labels and paths.** Owner: `internal/config`.
- **Rendering.** Owner: `internal/render`, pure functions from records to text or JSON, with the current time passed in.
- **The popup.** Owner: `~/dotfiles/tmux/.config/tmux/scripts/tmux-steps.sh`. It owns fzf, the key bindings and pane switching; it calls the command for every line it shows.

### API

```text
claude-steps show [<pane>|<session>] [--json]   one session; default is $TMUX_PANE
claude-steps board [--json] [--ids]             every live Claude pane
claude-steps note <pane>|<session> <text…>      append a note
claude-steps check                              format drift report
```

`<pane>` is a tmux pane id (`%12`). `<session>` is a full session id or a prefix of at least eight characters; the sessions it can match are those with a transcript or a notes file on this machine, and a prefix that matches two is refused with both listed.

`board --ids` starts every line with the pane id, a tab, the session id and a tab; both are empty on the header line. The popup previews and annotates by the session id on the row, so a note typed against a row lands in the session that row showed even if the pane has moved to another session meanwhile.

**The record** a caller receives for one session:

- identity: session id, transcript path, title, working directory, git branch, time of the last conversation row, all as last seen in the transcript;
- read status (table below) and the count of undecodable lines;
- events in time order, with ties in file order;
- notes in file order, the count of unreadable note lines, and the error if the notes file itself could not be read;
- signals: per fact, the reader's count, the second trace's count, and the misses.

Pull requests and compactions are events; the record offers them as lists too. `--json` prints the record with `pull_requests`, `compactions` and `labels` beside `events` and `notes`, every time as RFC 3339.

**Events.** Each has a time and one of these kinds:

| kind | lifted from | carries |
|---|---|---|
| skill | a slash command row whose next user row is its expansion; or a `Skill` tool call | name, arguments, typed or called by the model, whether the call failed |
| read | a `Read` tool call on a path ending `skills/<name>/SKILL.md` that returned no error | the skill's name |
| snippet | a human prompt containing the opening of a TabType snippet | snippet key |
| mention | a human prompt that names a labelled skill and holds no snippet; or a slash command for a labelled skill with no expansion | the opening 60 characters, the skills named |
| round | an `envoy run <job>`, joined to the collects of that job that follow | job name, whether it was dispatched here, collect time, envoy's status word |
| commit | a `git commit` whose call returned no error | subject, amend, directory when it is not the session's own |
| pr | a pull-request link row | repository, number, URL, opened here or linked |
| compaction | a compaction boundary in the main conversation | trigger |
| note | the notes store | text |

An event's time is its row's timestamp; for a tool call that is the call, not the result.

**The reader's rules.** Each is pinned by the obligation named.

- *Skill, typed.* The row holding `<command-name>/x</command-name>` is a skill when the next user row is a meta row under the same `promptId` that does not open with `<`. The name and arguments come from the command row, never from the expansion's path. A command with any other next row is a Claude Code built-in or a mod command and is not an event. (1)
- *Skill, called.* A `Skill` tool call is an event when it is made; a result marked as an error marks it failed. (2)
- *Human prompt.* A user row whose `origin.kind` is `human`. Rows with no `origin` are read as his only in a transcript where no row carries one, and never when they are compaction summaries, tool results, interruption markers or tagged rows. (8, 18)
- *Snippet.* The prompt and the snippet are lowercased with whitespace collapsed; the snippet's first 80 characters must appear in the prompt. A snippet shorter than 40 characters is never matched.
- *Mention.* A hyphenated skill name counts wherever it stands as a word. A one-word name such as `review` counts only as `/review`, inside a `skills/review/` path, or as "review skill". A prompt that holds a snippet yields no mention. (18)
- *Command position.* A Bash call is split into simple commands. Leading variable assignments and the words `command`, `exec` and `env` are skipped; a function defined in the call runs where it is called, with the call's here-document as its input; a word that is one variable assigned earlier in the call is replaced by its value as one word. (4, 6)
- *Commit.* `git`, any of its own options, then `commit`, without `--dry-run`. The subject is the first line of `-m`, `--message`, or a here-document on `-F -`. A call that returned an error yields no commit unless its output holds git's own `[branch sha]` line. A call with no result yet yields none. (6)
- *Round.* `envoy run <job>` adds a round at the dispatch. `envoy collect <job>` joins the latest round of that name; a later collect that printed a status replaces an earlier one, and one that printed none leaves it. `collect --status-only` is a probe and joins nothing. A job given as a directory path is reduced to its name without the `+N` reuse suffix. A collect of a job this session never ran is a round of its own, dated at the collect. (3, 4)
- *Pull request.* One event per URL, at its first link row. "Opened here" only when a `gh pr create` call that returned no error printed that URL. (7)
- *Compaction.* A `compact_boundary` system row outside a sidechain. The attachment restating invoked skills and the summary row are not events. (8)
- *Label.* A label matches skill names without their plugin prefix, snippet keys and envoy job-name prefixes. Its latest event is the latest skill, read, snippet or round that matches and did not fail; a mention only when there is none. Commits are counted from that event's time and never for a mention. (3, 18)

**Read status of a record:**

| status | entered when | what the views show |
|---|---|---|
| ok | the file was read and every line decoded | the timeline |
| partial | the file was read and some lines failed to decode | the timeline and the failed-line count |
| unreadable | the file cannot be opened, or holds lines of which none decodes as a row | `transcript unreadable` and the notes |
| missing | no file named `<session id>.jsonl` exists under the projects directory | `no transcript` and the notes |

A line fails to decode when it is not JSON, when it has no `type`, when a field the reader uses has another type, or when it is a conversation row with no timestamp. A last line with no newline that does not decode is still being written and is not counted. An empty file, and a file with rows and no conversation yet, are `ok` with no events. A set-aside copy (`<id>.orphaned-…jsonl`, `<id>.jsonl.superseded-…`) is never matched, because the lookup is by whole file name. When two project directories hold the same file name, the one modified last is read.

**Signals.** The decoder counts each fact twice as it reads:

| fact | the reader's rule | the second trace | a miss |
|---|---|---|---|
| skill, typed | a command row followed by its expansion | a meta row opening "Base directory for this skill:" with no tool call behind it | such a row that follows no command |
| skill, model call | a `Skill` tool call | such a row naming a tool call | one naming a call the reader did not see |
| human prompt | `origin.kind` is `human` | `promptSource` is `typed` or `queued` | a typed row with another origin |
| envoy round | `envoy run` or `collect` in command position | a result holding envoy's `job:` and `status:` lines | such a result for a call that names envoy and parsed as none |
| commit | `git commit` in command position | a result holding git's `[branch sha]` line | such a result for a call that says "commit" and parsed as none |
| pull request | a link row | a URL returned by `gh pr create` | a URL with no link row |
| compaction | a boundary row | a compaction summary row | summaries beyond the boundaries |

A second trace is evidence, not a superset. Claude Code itself now and then leaves a created pull request without a link row (2 of 252 across the laptop's transcripts, by the Opus voice's count in consult round 2), which is why `check` tolerates one miss in ten.

**Configuration** (`~/.config/claude-steps/config.toml`, optional):

```toml
projects_dir = "~/.claude/projects"            # default
snippets     = "~/.config/tabtype/config.toml" # default; absent file means no snippet events

[[label]]
name = "review"
skills = ["review"]
snippets = ["review-implementation", "review-implementation-again", "review-verify"]
jobs = ["review-"]
count_commits = true
```

With no file there are no label columns. The binary ships no labels: skill names are personal and live in the dotfiles. An unknown key is an error, so a misspelt one cannot leave a column silently empty.

**Time.** Human output shows relative times ("15 minutes ago", "2 hours ago") from `github.com/dustin/go-humanize`'s `RelTime`, with the current time passed in by the caller.

### Wiring

- **Read path.** Each invocation reads tmux, resolves the session, loads records through the loader, prints, and exits. Nothing persists between invocations. The board loads its sessions concurrently.
- **Pane to session.** One `tmux list-panes -a` call reading the pane option `@claude_ctx_sid`, which the dotfiles context chip publishes for every live Claude pane. An id that is not a well-formed session id is treated as no session.
- **Session to file.** A stat of `<session id>.jsonl` in every directory under `projects_dir`.
- **Note path.** `claude-steps note` appends one line, `{"at": <RFC 3339>, "text": <string>}`, to `$XDG_STATE_HOME/claude-steps/notes/<session id>.jsonl` (default `~/.local/state`), opened in append mode and written in one call. If the file's last byte is not a newline, the line starts with one, so a write the system once cut short stays one unreadable line and never swallows the next note.
- **Failure.** Every failure is a message on stderr and a non-zero exit.
- **The popup** (dotfiles, `prefix S`): the key runs `tmux-steps.sh`, which opens a popup with fzf over `claude-steps board --ids` and hides the two id fields. The preview is `claude-steps show <session id>`, scrolled to its end. The cursor starts on the origin pane's row and stays where it is when a note reloads the list. Enter switches the client to the row's pane if the pane still exists. ctrl-n takes one line through fzf used as a text field, as `~/dotfiles/tmux/.config/tmux/scripts/tmux-rename-pane.sh` does, and passes it to `claude-steps note <session id>`. Its design notes are in `~/dotfiles/tmux/.config/tmux/scripts/steps.md`, and `~/dotfiles/tmux/.config/tmux/scripts/tests/test-steps-popup.py` pins these behaviours on a private tmux socket.

### Rejected shapes

- **A recorder inside the session** (constraint: see events as they happen). Lost because its commands write rows the model reads, its store is per account, and it only knows sessions started after it loaded.
- **A state per check, with user marks** (constraint: answer "did X run" with a glyph). Lost because a load is not an outcome; marks then outrank later evidence. Notes cover the correction case.
- **A key-driven screen inside the binary** (constraint: one self-contained program). Lost because screens cannot be tested with fixtures and the dotfiles popups already use fzf.
- **Envoy's job store or the Obelisk index as the source** (constraint: reuse existing records). Lost because the transcript already holds each round and the status of each collect, and a second source is a second format to track.
- **A version constant the views compare against** (constraint: flag a Claude Code the reader was never checked on). Lost because Claude Code updates most days and the line would always show; the signals report drift only when there is some.
- **Splitting a variable's value into a command** (`C="git commit -q"; $C -m x`). Lost because the Bash tool runs zsh on these machines, which does not split an unquoted variable: in the sampled session that call failed and the model retried with a function.

### Premises

**Decision: read transcript files directly.** Settled.
Basis, established from the vendor documentation (code.claude.com/docs/en/sessions, read 2026-10-02): transcripts are JSONL at `~/.claude/projects/<project>/<session-id>.jsonl`; "the entry format is internal to Claude Code and changes between versions, so scripts that parse these files directly can break on any release"; transcripts are deleted after 30 days by default; a fork copies the conversation history.
Basis, measured 2026-10-02 with the built reader over the 217 laptop transcripts changed in the last week (Claude Code 2.1.251 to 2.1.287): no fact's second trace saw anything the reader's rule missed. Counts, reader then second trace: typed skills 198 and 197, model skill calls 98 and 98, human prompts 624 and 622, envoy calls 241 and 88, commits 649 and 14, pull requests 40 and 32, compactions 30 and 30.
Does not establish: the shape on later versions.
Fallback: the format knowledge has one owner, an unrecognised file is `unreadable`, and the signals report drift on the view.

**Decision: decode every line structurally, with no text prefilter.** Settled.
Basis, measured 2026-10-02, laptop, warm file cache: the board of nine live sessions prints in 0.3 s; `check` reads 217 transcripts in 0.7 s; the largest transcript on disk (32 MB, longest line 1.27 MB) decoded in 64 ms in a spike.
Does not establish: cold-cache or mini timings.
The reader uses no line scanner with a fixed token limit; lines exceed 1 MB.

**Decision: resolve a pane through `@claude_ctx_sid`.** Settled.
Basis, established from source: `~/dotfiles/tmux/.config/tmux/scripts/lib/agent-vocab.sh` names it as the owner option of the context chip. Measured 2026-10-02: nine of nine Claude panes on the laptop carried an id that resolved to a transcript; seven of seven on the mini did in the prototype.
Does not establish: behaviour where the chip is not installed. There the board is empty and `show <session id>` still works.

**Decision: find the transcript by session id across all project directories.** Settled.
Basis, observed 2026-10-02: after `/cd`, this session's transcript moved from the old project directory to the new one under the same file name, and the built reader followed it.

**Decision: a slash command is a skill when the next user row is its expansion.** Settled.
Basis, counted by the Opus voice in consult round 2 over 710 laptop transcripts, not re-run: 1,354 of 1,358 skill command rows are followed directly by their expansion, and 20 built-in command rows share a `promptId` with an expansion. Observed in one session here: a `Skill` call later in the turn that `/compact` opened carried the same `promptId`, so matching on the id alone would make `/compact` a skill.
Does not establish: the other four rows; they show as nothing, or as a mention when the skill is labelled.

**Decision: human prompts are user rows whose `origin.kind` is `human`.** Settled.
Basis, measured on six transcripts: typed and queued prompts carry it; task notifications carry `task-notification`; built-in commands, interruption markers, compaction summaries and scheduled slash commands carry none. Counted by the Opus voice in consult round 2, not re-run: all 172 compaction summaries on the laptop are non-meta rows with no origin, and 151 name a tracked skill, so a per-row fallback would invent a mention after every compaction.

**Decision: a round is read from `envoy run` and `envoy collect` calls.** Settled.
Basis, counted by the Opus voice in consult round 2 over 725 collects on the laptop, not re-run: 537 print `ok`, 139 print no status line (redirected, piped or `--result-only`), 49 print `partial`, `failed`, `running`, `infra`, `no-result`, `timeout` or `interrupted`. 121 Bash commands hold `envoy run` as text without running it. 23 sessions dispatch one job name twice. Observed here: a session whose collect printed `running`, and which then read the result through a script that printed no status.
Does not establish: rounds dispatched some other way. They appear only as the skill event that started them.

**Decision: a commit is read from the command, and its success from the call.** Settled, with limits.
Basis, measured: with `git commit -q` the result holds no subject, so the result cannot be the source. Messages arrive as `-m "…"`, as `-m "$(cat <<'EOF' …)"`, as `-F - <<'EOF'`, and through a function defined in the same call.
Limits: a call ending `; true` after a failed commit counts as a commit; commits made by `git merge`, `rebase` or `cherry-pick`, by a script, or by a subagent are not listed. `--help` says so.

**Decision: events from a rewound branch of the conversation stay in the timeline.** Assumed.
Basis: a review that ran or a commit that was made is a fact whatever the conversation later did. Not sampled: how a rewound branch appears in the file.
Fallback: if rewound branches prove confusing in use, follow the parent chain from the last row. The record's shape does not change.

**Limit: subagent work is not read.** Subagent transcripts live under `<session id>/subagents/` and are not opened; rows marked as sidechain in the main file are skipped, including compactions inside them.

**Dependencies.** `github.com/BurntSushi/toml` for both TOML files, as `gwt` uses, and `github.com/dustin/go-humanize` for relative time.

## Verification

Each obligation names the test that pins it. For each of obligations 1 to 15 and 17 to 19, one of its rules was removed from the code and the suite went red; 16 is an observation by hand. Run `make check`.

Fixtures are built by `internal/fixture`, which copies the shape of real rows and carries invented text; no private transcript is committed. Tests run the real record loader over a temporary home. tmux is substituted by a fixed pane list at the one function that calls it; that substitute cannot prove the `list-panes` format string, which obligation 16 covers.

1. A slash command followed by its expansion, and a `Skill` tool call, each yield one skill event with its arguments. A built-in yields none, including one whose turn continues with a reminder row and one that shares a prompt id with a later expansion. An expansion whose path has no `skills/` segment still yields the invoked name. Pinned: `TestSkillIsNamedByItsInvocation`, `TestBuiltinSharingAPromptIDWithALaterExpansionIsNotASkill`.
2. A `Skill` call whose result is an error is marked failed, rendered `failed to load`, and is not a label's latest event. Pinned: `TestFailedSkillCall`.
3. `envoy run review-r1`, a commit, a collect, then `envoy run review-r2` and a commit: two rounds, the first collected, the second dispatched; "commits since" for the review label is 1. Pinned: `TestRoundsAndTheCommitsSince`, `TestShow`.
4. A collect keeps envoy's status word; a fan-out collect's first status line is the one read; a status probe joins nothing; a collect with no status keeps the word already read; the same name dispatched twice is two rounds; a path joins by job name; a collect with no run is its own round; text that only mentions envoy is nothing. Pinned: `TestCollectKeepsEnvoysWord`, `TestCollectWithNoStatusKeepsTheStatusAlreadyRead`, `internal/shell` tests.
5. Every label cell fits the grammar in § Behaviour. The tool's own words in a board or session view hold no tick and none of "done", "passed", "stale", "fresh", "complete", "succeeded"; his own words that do are shown as written. Pinned: `TestBoard`, `TestNoVerdictInTheToolsOwnWords`.
6. `git log --grep=commit`, `git grep commit`, quoted text, a call that returned an error, `--dry-run` and a call with no result yield no commit. A plain message, `git -C <dir>`, a here-document, a substituted here-document holding quotes and `&&`, `--amend`, a commit followed by a failing command, and a function called twice each yield theirs, with the subject. Pinned: `TestCommitIsACommandNotAWord`, `internal/shell` tests.
7. Two pull-request links in two repositories are both kept. A link whose URL `gh pr create` returned is "opened here"; a link after `git push`, or after a `gh pr create` that failed with "already exists", is "linked". A repeated link row adds nothing. Pinned: `TestPullRequests`.
8. The attachment restating invoked skills and the compaction summary yield no event, whatever skills they name. A boundary inside a sidechain is not counted; one in the main conversation is, with its trigger. Rows with no origin are his prompts only in a transcript where none carries one. Pinned: `TestCompaction`, `TestOnlyHumanPromptsAreRead`.
9. A file with JSON spaced and ordered differently from Claude Code's output, one malformed line and one 1.3 MB line reads as `partial` with one failed line and the same events as its compact form. Pinned: `TestSpacingMalformedAndLongLines`.
10. A file no line of which decodes, and one whose rows carry no type, are `unreadable`; a session id with no file is `missing`; both render their message and never an empty timeline. An empty file, a session not yet prompted and a half-written last line are `ok`. A set-aside copy is not matched. Pinned: `TestReadStatus`, `TestUnreadTranscriptsSayWhy`, `TestBoard`.
11. Sixty-four notes appended at the same moment are all kept. A malformed line in a notes file is counted and the other notes are shown. A note appended after a torn write is not glued to it. Pinned: `internal/notes` tests, `TestNote`.
12. `show` and `board` render a session's notes when its transcript is missing. Pinned: `TestUnreadTranscriptsSayWhy`, `TestBoard`.
13. An eight-character prefix that matches two sessions is refused with both listed; a session with only notes resolves; a malformed id is refused. Only `internal/record` reads the projects directory. Pinned: `TestResolve`, `TestAmbiguousPrefixIsRefused`, `TestOneReaderAndOneReadOnlyProcess`.
14. Running every command over a fixture home leaves every existing file byte-identical with its modification time, and creates files only in the notes directory. The only package that can start a process is `internal/panes`, and its one call is `tmux list-panes -a -F`. Pinned: `TestOnlyTheNotesAreWritten`, `TestOneReaderAndOneReadOnlyProcess`.
15. The binary's import graph contains no `net` package. Pinned: `TestNoNetworkInTheBinary`.
16. On this machine, against real sessions: `board` lists the live Claude panes and `show` on one prints its timeline. Observed by hand on the laptop on 2026-10-02, nine panes; not a test. The mini's run is the dotfiles wiring's check.
17. Rendering with a fixed current time prints `15 minutes ago` and `2 hours ago` for events that old, and `--json` prints timestamps with no relative strings. Pinned: `TestShow`, `TestShowJSON`.
18. A prompt that names a labelled skill yields a mention with its opening words; "review" as plain English does not; a slash command with an expansion and a pasted snippet yield no mention; a labelled slash command with no expansion does. A label with only a mention renders `named <time>` and has no commit count; a read ranks with a run. Pinned: `TestMentions`, `TestMentionOnlyLabelHasNoCommitCount`, `TestBoard`.
19. Each of the seven facts counts a miss when its second trace has no match, and none on a consistent transcript. The view says so. `check` exits non-zero on a set where a created pull request has no link row or a transcript is unreadable, zero on a consistent set, and reads nothing older than seven days. Pinned: `TestSignalsCountWhatTheReaderMissed`, `TestViewSaysWhenTheReaderMayHaveMissedSomething`, `TestCheck`.

Also pinned: events sort by time with ties in file order (`TestEventsAreInTimeOrderWithFileOrderOnTies`); a transcript is found in whichever project directory holds it, the newest when two do (`TestTranscriptIsFoundByIDInAnyProject`); a run of equal timeline lines prints once with its count (`TestRepeatedLinesAreOneLine`); an unknown configuration key is refused (`internal/config` tests).

Limit: fixtures prove the rules against the shapes sampled on 2026-10-02. They do not prove that tomorrow's Claude Code writes those shapes; obligation 16 and the signals are the only observations of the live format.

## Delivery

**Repository boundary.** Two repositories change, so this is two deliveries in a fixed order.

1. `claude-steps` (this repository): the command, its tests, `make check` and `make install`, and the README. Done, and installed on the laptop.
2. The dotfiles:
   - done: the popup script, its `prefix S` key, its design note and its test in the tmux package; the `claude-steps` stow package holding `config.toml` with the labels; `mini-sync` carrying the binary and the package; `claude-tomini` merging the session's notes into the mini's; the board in the tmux workflow document and the testing routes; `~/dotfiles/docs/qiushi-mini.md` saying what the `tabtype` package is now stowed for;
   - open: the first `prefix S` on the mini after `mini-sync` has run there;
   - open, last: removal of the `steps` mod: its directory, its entry in `CLAUDE_CODE_PLUGIN_DIRS` in `~/dotfiles/claude/.claude/settings.json`, its section and its "sessions already running" note in `~/dotfiles/docs/claude-mods.md`, the `steps` line in `~/dotfiles/CLAUDE.md`, and that document's mods-loaded probe moved from `/did` to another mod's command.

The mod is removed after the key has shown a real session on both machines. Sessions already running keep the mod until they restart.

**After it ships.** Re-run the session-history query for "did we / have we" step questions (`q-candidates.mjs` in the design session's scratchpad) over a calendar month of sessions started after the key exists, and report the rate per long session beside September 2026's: 6 questions across 170 long sessions on the laptop. The rate says whether the questions still get asked. It does not say whether the view was opened.

**Defaults, confirmed by Qiushi on 2026-10-02.** The key is `prefix S`. The labels are consult, review, verify, docs and prompts, with handoff skills under docs and the `handoff-for-review` snippet under no label, since it asks a later session to review.
