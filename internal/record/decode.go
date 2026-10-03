package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/qiushiyan/claude-steps/internal/config"
	"github.com/qiushiyan/claude-steps/internal/shell"
)

// This file is the only place that knows what a transcript row looks like.
// Claude Code documents the format as internal and free to change, so every
// rule here decodes JSON structurally and none depends on how a row is
// spelled on disk. The shapes were sampled from Claude Code 2.1.251 to
// 2.1.287.

// row is a transcript line, reduced to the fields the reader uses. A line
// whose fields have another type fails to decode and is counted as unread.
type row struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	Timestamp        string `json:"timestamp"`
	IsSidechain      bool   `json:"isSidechain"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	PromptID         string `json:"promptId"`
	SourceToolUseID  string `json:"sourceToolUseID"`
	PromptSource     string `json:"promptSource"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	AiTitle         string `json:"aiTitle"`
	PrNumber        int    `json:"prNumber"`
	PrURL           string `json:"prUrl"`
	PrRepository    string `json:"prRepository"`
	CompactMetadata struct {
		Trigger string `json:"trigger"`
	} `json:"compactMetadata"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

const (
	// The line a skill's expansion opens with. A slash command is a skill run
	// when a row like this follows it; a built-in command has none.
	skillExpansion = "Base directory for this skill:"

	openingWords = 60  // runes of a prompt shown for a mention
	subjectMax   = 100 // runes of a commit subject kept
	argsMax      = 160 // runes of a skill's arguments kept
)

var (
	commandName = regexp.MustCompile(`<command-name>/([^<\s]+)</command-name>`)
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	// "status: ok", "status: partial — 1 of 2 …": the word envoy opens with.
	statusLine = regexp.MustCompile(`(?m)^status:[ \t]*([A-Za-z][\w-]*)`)
	jobLine    = regexp.MustCompile(`(?m)^job:[ \t]*\S`)
	// "[main 1a2b3c4] subject", the line git prints for a commit it made.
	gitSummary = regexp.MustCompile(`(?m)^\[[^\]\s]+( \([^)]*\))? [0-9a-f]{7,40}\] `)
	pullURL    = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)
	jobReuse   = regexp.MustCompile(`\+\d+$`)
	skillFile  = regexp.MustCompile(`(?:^|/)skills/([^/]+)/SKILL\.md$`)
	envoyCall  = regexp.MustCompile(`\benvoy\s+(run|collect)\b`)
)

// mention matches a prompt that names a skill without running it.
type mention struct {
	name string
	re   *regexp.Regexp
}

// compileMentions builds the matchers for the labelled skill names. A
// hyphenated name counts wherever it stands as a word. A one-word name such
// as "review" is ordinary English, so it counts only as "/review", inside a
// "skills/review/" path, or as "review skill".
func compileMentions(names []string) []mention {
	const edge, end = `(^|[^\w/-])`, `($|[^\w-])`
	var out []mention
	for _, name := range names {
		n := regexp.QuoteMeta(name)
		var pattern string
		if strings.Contains(name, "-") {
			pattern = `(^|[^\w-])` + n + end
		} else {
			pattern = edge + `/` + n + end + `|skills/` + n + `/|` + edge + n + ` skill\b`
		}
		out = append(out, mention{name: name, re: regexp.MustCompile(`(?i)` + pattern)})
	}
	return out
}

type bashCall struct {
	at             time.Time
	commits        []commitCommand
	runs           []int    // indexes of the round events this call dispatched
	collects       []string // job arguments of `envoy collect`
	createsPR      bool
	probes         int // `envoy collect --status-only` calls
	mentionsCommit bool
	mentionsEnvoy  bool
}

type slashCommand struct {
	at       time.Time
	name     string
	args     string
	promptID string
}

type prompt struct {
	at   time.Time
	text string
}

type decoder struct {
	snippets []config.Snippet
	mentions []mention

	labelled map[string]bool // the skill names some label lists

	rec        *Record
	lines      int
	recognised int // rows that decode and carry a type

	lastAt     time.Time
	cwd        string
	skillCalls map[string]int       // Skill tool call id → its event
	reads      map[string]Event     // Read call id → the skill file it read
	bash       map[string]*bashCall // Bash tool call id → what it ran
	rounds     map[string]int       // envoy job → its latest round event
	prs        map[string]int       // pull request URL → its event
	created    map[string]bool      // URLs `gh pr create` returned
	slash      *slashCommand        // a slash command waiting for its expansion
	sawOrigin  bool
	unsourced  []prompt // prompts with no origin, used when no row carries one

	sig struct{ slash, tool, pr, compaction, human, commit, round Signal }
}

func newDecoder(rec *Record, snippets []config.Snippet, mentions []mention) *decoder {
	labelled := map[string]bool{}
	for _, m := range mentions {
		labelled[m.name] = true
	}
	return &decoder{
		snippets:   snippets,
		mentions:   mentions,
		labelled:   labelled,
		rec:        rec,
		skillCalls: map[string]int{},
		reads:      map[string]Event{},
		bash:       map[string]*bashCall{},
		rounds:     map[string]int{},
		prs:        map[string]int{},
		created:    map[string]bool{},
	}
}

// read decodes the whole transcript. A row counts as recognised only once
// every field the reader takes from it has decoded, so a format change that
// keeps the row types and moves their fields still reads as unread lines. Lines run past a megabyte, so it reads
// with no fixed line limit.
func (d *decoder) read(r io.Reader) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		complete := err == nil
		if line = bytes.TrimSpace(line); len(line) > 0 {
			// A last line with no newline is still being written; one that
			// does not decode yet is neither a line read nor a damaged one.
			switch {
			case d.line(line):
				d.lines++
				d.recognised++
			case complete:
				d.lines++
				d.rec.UnreadLines++
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (d *decoder) add(e Event) int {
	d.rec.Events = append(d.rec.Events, e)
	return len(d.rec.Events) - 1
}

func (d *decoder) line(raw []byte) bool {
	var r row
	if err := json.Unmarshal(raw, &r); err != nil || r.Type == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		at = d.lastAt
	}

	switch r.Type {
	case "ai-title":
		if r.AiTitle != "" {
			d.rec.Title = r.AiTitle
		}
	case "pr-link":
		if _, seen := d.prs[r.PrURL]; !seen && r.PrURL != "" {
			d.prs[r.PrURL] = d.add(Event{At: at, Kind: PR, Repo: r.PrRepository, Number: r.PrNumber, URL: r.PrURL})
		}
	case "system":
		if r.Subtype == "compact_boundary" && !r.IsSidechain {
			d.add(Event{At: at, Kind: Compaction, Trigger: r.CompactMetadata.Trigger})
			d.sig.compaction.Primary++
		}
	case "user", "assistant":
		// A conversation row with no time cannot be placed in a timeline.
		if err != nil {
			return false
		}
		text, blocks, ok := content(r.Message.Content)
		if !ok {
			return false
		}
		d.rec.Turns++
		// Subagent turns recorded in the main file are not the session's own.
		if r.IsSidechain {
			return true
		}
		d.lastAt, d.rec.LastAt = at, at
		if r.Cwd != "" {
			d.cwd, d.rec.Cwd = r.Cwd, r.Cwd
		}
		if r.GitBranch != "" {
			d.rec.Branch = r.GitBranch
		}
		if r.Type == "assistant" {
			return d.assistant(at, blocks)
		} else {
			d.user(at, r, text, blocks)
		}
	}
	return true
}

// content reads a message's content, which is a string or a list of blocks.
func content(raw json.RawMessage) (text string, blocks []block, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, true
	}
	if raw[0] == '"' {
		return text, nil, json.Unmarshal(raw, &text) == nil
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", nil, false
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), blocks, true
}

// assistant records the tool calls of a model turn. A call to a tool the
// reader knows whose input does not decode makes the row unread.
func (d *decoder) assistant(at time.Time, blocks []block) bool {
	ok := true
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		switch b.Name {
		case "Skill":
			var in struct {
				Skill string `json:"skill"`
				Args  string `json:"args"`
			}
			if json.Unmarshal(b.Input, &in) != nil || in.Skill == "" {
				ok = false
				continue
			}
			d.skillCalls[b.ID] = d.add(Event{At: at, Kind: Skill, Via: "tool", Name: in.Skill, Args: clip(in.Args, argsMax)})
			d.sig.tool.Primary++
		case "Bash":
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &in) != nil {
				ok = false
				continue
			}
			d.bash[b.ID] = d.bashCommand(at, in.Command)
		case "Read":
			// Asked to "follow skills/x/SKILL.md", the model reads the file
			// and no skill loads. The read is the only trace of it.
			var in struct {
				FilePath string `json:"file_path"`
			}
			if json.Unmarshal(b.Input, &in) != nil {
				ok = false
				continue
			}
			if m := skillFile.FindStringSubmatch(in.FilePath); m != nil {
				d.reads[b.ID] = Event{At: at, Kind: Read, Name: m[1]}
			}
		}
	}
	return ok
}

// within resolves git's -C argument against where the command runs.
func within(dir, arg string) string {
	switch {
	case arg == "":
		return dir
	case path.IsAbs(arg) || strings.HasPrefix(arg, "~") || dir == "":
		return path.Clean(arg)
	}
	return path.Join(dir, arg)
}

// bashCommand records what a Bash call ran. Round events are added now, at
// the dispatch; commits wait for the result, which says whether they happened.
func (d *decoder) bashCommand(at time.Time, command string) *bashCall {
	call := &bashCall{at: at, mentionsCommit: strings.Contains(command, "commit"), mentionsEnvoy: envoyCall.MatchString(command)}
	// Text that does not parse yields no commands; a commit or round in it
	// shows as a miss in the second traces below.
	cmds, _ := shell.Split(command, d.cwd)
	for _, c := range cmds {
		argv := c.Argv()
		if commit, ok := commitOf(argv, c); ok {
			if commit.dir = within(c.Dir, commit.dir); commit.dir == d.cwd {
				commit.dir = ""
			}
			call.commits = append(call.commits, commit)
		}
		if job, ok := envoyJob(argv, "run"); ok {
			idx := d.add(Event{At: at, Kind: Round, Name: job, Dispatched: true})
			d.rounds[job] = idx
			call.runs = append(call.runs, idx)
			d.sig.round.Primary++
		}
		// `collect --status-only` asks whether the job is still running; it
		// delivers no result, so it is not a collect.
		if job, ok := envoyJob(argv, "collect"); ok {
			if slices.Contains(argv, "--status-only") {
				call.probes++
			} else {
				call.collects = append(call.collects, job)
			}
			d.sig.round.Primary++
		}
		if len(argv) >= 3 && argv[0] == "gh" && argv[1] == "pr" && argv[2] == "create" {
			call.createsPR = true
		}
	}
	return call
}

func (d *decoder) user(at time.Time, r row, text string, blocks []block) {
	if r.Origin != nil {
		d.sawOrigin = true
	}
	results := false
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results = true
			d.result(at, b)
		}
	}
	waiting := d.slash
	d.slash = nil
	if results {
		// Only the row right after a command can be its expansion.
		if waiting != nil {
			d.unexpanded(waiting)
		}
		return
	}

	trimmed := strings.TrimSpace(text)
	follows := waiting != nil && r.IsMeta && !r.IsCompactSummary && waiting.promptID == r.PromptID
	if waiting != nil && !(follows && !strings.HasPrefix(trimmed, "<")) {
		d.unexpanded(waiting)
	}
	switch {
	case r.IsCompactSummary:
		d.sig.compaction.Second++
	case r.IsMeta:
		expansion := strings.HasPrefix(trimmed, skillExpansion)
		switch {
		case expansion && r.SourceToolUseID != "":
			d.sig.tool.Second++
			if _, known := d.skillCalls[r.SourceToolUseID]; !known {
				d.sig.tool.Missed++
			}
		case expansion:
			d.sig.slash.Second++
			if !follows {
				d.sig.slash.Missed++
			}
		}
		// The skill's name comes from the command the user typed. The
		// expansion only proves that the command was a skill.
		if follows && !strings.HasPrefix(trimmed, "<") {
			d.add(Event{At: waiting.at, Kind: Skill, Via: "slash", Name: waiting.name, Args: clip(waiting.args, argsMax)})
			d.sig.slash.Primary++
		}
	default:
		human := r.Origin != nil && r.Origin.Kind == "human"
		typed := r.PromptSource == "typed" || r.PromptSource == "queued"
		if typed {
			d.sig.human.Second++
			if !human {
				d.sig.human.Missed++
			}
		}
		if m := commandName.FindStringSubmatch(text); m != nil {
			args := ""
			if a := commandArgs.FindStringSubmatch(text); a != nil {
				args = strings.Join(strings.Fields(a[1]), " ")
			}
			d.slash = &slashCommand{at: at, name: m[1], args: args, promptID: r.PromptID}
			return
		}
		if trimmed == "" {
			return
		}
		switch {
		case human:
			d.sig.human.Primary++
			d.prompt(at, text)
		case r.Origin == nil:
			d.unsourced = append(d.unsourced, prompt{at, text})
		}
	}
}

// unexpanded handles a slash command that no expansion followed. That is a
// built-in or a mod command, which is not an event, unless it names a
// labelled skill: then the user asked for the skill and no load was seen,
// which is shown as his words, like any other mention.
func (d *decoder) unexpanded(c *slashCommand) {
	name := bareSkill(c.name)
	if d.labelled[name] {
		d.add(Event{At: c.at, Kind: Mention, Names: []string{name}, Text: clip(strings.TrimSpace("/"+c.name+" "+c.args), openingWords)})
	}
}

// prompt lifts what a human prompt shows: a pasted snippet, or failing that
// the labelled skills it names.
func (d *decoder) prompt(at time.Time, text string) {
	flat := config.Squash(text)
	pasted := false
	for _, s := range d.snippets {
		if strings.Contains(flat, s.Head) {
			d.add(Event{At: at, Kind: Snippet, Name: s.Key})
			pasted = true
		}
	}
	if pasted {
		return
	}
	var names []string
	for _, m := range d.mentions {
		if m.re.MatchString(text) {
			names = append(names, m.name)
		}
	}
	if len(names) > 0 {
		d.add(Event{At: at, Kind: Mention, Names: names, Text: clip(strings.Join(strings.Fields(text), " "), openingWords)})
	}
}

func (d *decoder) result(at time.Time, b block) {
	if idx, ok := d.skillCalls[b.ToolUseID]; ok {
		if b.IsError {
			d.rec.Events[idx].Failed = true
		}
		return
	}
	if read, ok := d.reads[b.ToolUseID]; ok {
		delete(d.reads, b.ToolUseID)
		if !b.IsError {
			d.add(read)
		}
		return
	}
	call := d.bash[b.ToolUseID]
	if call == nil {
		return
	}
	delete(d.bash, b.ToolUseID)
	text := resultText(b.Content)

	// A call that returned an error made no commit, unless git's own summary
	// line is in the output: the commit succeeded and a later command failed.
	committed := gitSummary.MatchString(text)
	if len(call.commits) > 0 && (!b.IsError || committed) {
		for _, c := range call.commits {
			d.add(Event{At: call.at, Kind: Commit, Text: c.subject, Amend: c.amend, Dir: c.dir})
			d.sig.commit.Primary++
		}
	}
	if committed {
		d.sig.commit.Second++
		if len(call.commits) == 0 && call.mentionsCommit {
			d.sig.commit.Missed++
		}
	}

	if b.IsError {
		for _, idx := range call.runs {
			d.rec.Events[idx].Failed = true
		}
	}
	outcome := ""
	if m := statusLine.FindStringSubmatch(text); m != nil {
		outcome = m[1]
	} else if b.IsError {
		outcome = "error"
	}
	for _, job := range call.collects {
		d.collect(job, call.at, at, outcome)
	}
	if jobLine.MatchString(text) && statusLine.MatchString(text) {
		d.sig.round.Second++
		if len(call.collects)+len(call.runs)+call.probes == 0 && call.mentionsEnvoy {
			d.sig.round.Missed++
		}
	}

	if call.createsPR && !b.IsError {
		for _, url := range pullURL.FindAllString(text, -1) {
			d.created[url] = true
		}
	}
}

// collect joins a collect to the latest round dispatched under the job's
// name. A later collect that printed a status replaces an earlier one; one
// that printed none (`--result-only`, or output sent to a file) leaves the
// status already read. A job this session never dispatched becomes a round of
// its own, dated at the collect call.
func (d *decoder) collect(arg string, called, returned time.Time, outcome string) {
	job := d.jobName(arg)
	idx, ok := d.rounds[job]
	if !ok {
		idx = d.add(Event{At: called, Kind: Round, Name: job})
		d.rounds[job] = idx
	}
	e := &d.rec.Events[idx]
	if e.CollectedAt != nil && outcome == "" {
		return
	}
	e.CollectedAt, e.Outcome = &returned, outcome
}

// jobName reduces a collect's argument to a job name. envoy takes a name or
// a directory path; a path ends in the job's directory, which carries "+2",
// "+3" when the name was reused, or in a fan-out member's directory under it.
func (d *decoder) jobName(arg string) string {
	if !strings.Contains(arg, "/") {
		return arg
	}
	clean := path.Clean(arg)
	base := jobReuse.ReplaceAllString(path.Base(clean), "")
	parent := jobReuse.ReplaceAllString(path.Base(path.Dir(clean)), "")
	if _, ok := d.rounds[base]; !ok {
		if _, ok := d.rounds[parent]; ok {
			return parent
		}
	}
	return base
}

func (d *decoder) finish() {
	if d.slash != nil {
		d.unexpanded(d.slash)
	}
	// Transcripts from before Claude Code marked a prompt's origin: take the
	// plain text rows as the user's own.
	if !d.sawOrigin {
		for _, p := range d.unsourced {
			t := strings.TrimSpace(p.text)
			if strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") || strings.HasPrefix(t, "/") {
				continue
			}
			d.prompt(p.at, p.text)
		}
	}
	for url, idx := range d.prs {
		d.rec.Events[idx].OpenedHere = d.created[url]
	}
	d.sig.pr.Primary, d.sig.pr.Second = len(d.prs), len(d.created)
	for url := range d.created {
		if _, linked := d.prs[url]; !linked {
			d.sig.pr.Missed++
		}
	}
	if extra := d.sig.compaction.Second - d.sig.compaction.Primary; extra > 0 {
		d.sig.compaction.Missed = extra
	}
	slices.SortStableFunc(d.rec.Events, func(a, b Event) int { return a.At.Compare(b.At) })

	name := func(s Signal, fact string) Signal { s.Fact = fact; return s }
	d.rec.Signals = []Signal{
		name(d.sig.slash, "skill, typed"),
		name(d.sig.tool, "skill, model call"),
		name(d.sig.human, "human prompt"),
		name(d.sig.round, "envoy round"),
		name(d.sig.commit, "commit"),
		name(d.sig.pr, "pull request"),
		name(d.sig.compaction, "compaction"),
	}
}

// SignalNotes says, per fact, what the two counts are. `check` prints them.
var SignalNotes = map[string]string{
	"skill, typed":      "a slash command followed by its expansion / an expansion row with no tool call behind it",
	"skill, model call": "a Skill tool call / an expansion row that names a tool call",
	"human prompt":      "a prompt row with origin human / a row whose source is typed or queued",
	"envoy round":       "an envoy run or collect in command position / a result of an envoy call holding its job and status lines",
	"commit":            "a git commit in command position / a result holding git's commit summary line",
	"pull request":      "a pull-request link row / a URL returned by gh pr create",
	"compaction":        "a compaction boundary / a compaction summary row",
}

func resultText(raw json.RawMessage) string {
	text, _, _ := content(raw)
	return text
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}

type commitCommand struct {
	subject string
	amend   bool
	dir     string // the argument of git -C
}

// Options of git itself that take a separate value, before the subcommand.
var gitValueFlags = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true, "--config-env": true,
}

// commitOf reports whether a simple command is `git commit`, and reads the
// subject from the message when the command carries one.
func commitOf(argv []string, c shell.Command) (commitCommand, bool) {
	if len(argv) < 2 || argv[0] != "git" {
		return commitCommand{}, false
	}
	var out commitCommand
	i := 1
	for i < len(argv) && strings.HasPrefix(argv[i], "-") {
		if gitValueFlags[argv[i]] {
			if argv[i] == "-C" && i+1 < len(argv) {
				out.dir = argv[i+1]
			}
			i++
		}
		i++
	}
	if i >= len(argv) || argv[i] != "commit" {
		return commitCommand{}, false
	}
	message, have := "", false
	take := func(m string) {
		if !have {
			message, have = m, true
		}
	}
	args := argv[i+1:]
	for j := 0; j < len(args); j++ {
		a := args[j]
		value := func() (string, bool) {
			if j+1 < len(args) {
				j++
				return args[j], true
			}
			return "", false
		}
		switch {
		case a == "--":
			j = len(args)
		case a == "--dry-run":
			return commitCommand{}, false
		case a == "--amend":
			out.amend = true
		case a == "--message" || shortCluster(a, 'm'):
			if v, ok := value(); ok {
				take(v)
			}
		case strings.HasPrefix(a, "--message="):
			take(strings.TrimPrefix(a, "--message="))
		case a == "--file" || shortCluster(a, 'F'):
			if v, ok := value(); ok && v == "-" && c.HasStdin {
				take(c.Stdin)
			}
		case a == "--file=-":
			if c.HasStdin {
				take(c.Stdin)
			}
		case strings.HasPrefix(a, "-m") && !strings.HasPrefix(a, "--"):
			take(a[2:])
		}
	}
	for line := range strings.Lines(message) {
		if s := strings.TrimSpace(line); s != "" {
			out.subject = clip(s, subjectMax)
			break
		}
	}
	return out, true
}

// shortCluster reports whether arg is a run of short options ending in last,
// as "-am" ends in the option that takes the message.
func shortCluster(arg string, last byte) bool {
	if len(arg) < 2 || arg[0] != '-' || arg[len(arg)-1] != last {
		return false
	}
	for i := 1; i < len(arg); i++ {
		ch := arg[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z') {
			return false
		}
	}
	return true
}

// Options of `envoy run` that take a separate value.
var envoyValueFlags = map[string]bool{
	"--with": true, "--prompt-file": true, "--timeout-min": true, "--cwd": true,
	"--baseline": true, "--max-budget-usd": true, "--base": true,
}

// envoyJob returns the job argument of `envoy run` or `envoy collect`.
func envoyJob(argv []string, sub string) (string, bool) {
	if len(argv) < 3 || argv[0] != "envoy" || argv[1] != sub {
		return "", false
	}
	for i := 2; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") {
			if envoyValueFlags[argv[i]] {
				i++
			}
			continue
		}
		return argv[i], true
	}
	return "", false
}
