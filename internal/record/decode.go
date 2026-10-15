package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
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
	// ToolUseResult is what a tool returned, beside the text the model saw.
	// Its shape differs from tool to tool, so it is decoded only where a
	// rule reads it: a pointer's Read (pointedText).
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	// Attachment is the harness's addition to the conversation; a prompt
	// the session took mid-turn is one (decoder.attachment).
	Attachment json.RawMessage `json:"attachment"`
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
	// "[main 1a2b3c4] subject", the line git prints for a commit it made.
	gitSummary = regexp.MustCompile(`(?m)^\[[^\]\s]+( \([^)]*\))? [0-9a-f]{7,40}\] `)
	pullURL    = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/pull/(\d+)`)
	skillFile  = regexp.MustCompile(`(?:^|/)skills/([^/]+)/SKILL\.md$`)
	// Claude Code wraps text the user pasted in this tag. It is the harness's
	// mark, not the user's words.
	pastedTag = regexp.MustCompile(`</?pasted_content\b[^>]*>`)
	// The one line drive-session's send-turn types in place of a prompt too
	// long to type, which it writes to a file: the prompt is the file.
	pointerPrompt = regexp.MustCompile(`^Read (/.+) and do what it asks\.$`)
	// The second trace for a pointer: any one-line prompt that opens by
	// asking for a file to be read, so a reworded send-turn reads as missed
	// pointers rather than as nothing.
	pointerLike = regexp.MustCompile(`^Read [/~][^\n]*$`)
)

// mention matches a prompt that names a skill without running it.
type mention struct {
	name string
	re   *regexp.Regexp
}

// compileMentions builds the matchers for the labelled skill names. A
// hyphenated name counts wherever it stands as a word. A one-word name such
// as "review" is ordinary English, so it counts only as "/review", as the
// "skills/review/SKILL.md" file, or as "review skill". Another file in its
// directory is not the skill: a handoff's pickup names the handoff skill's
// pickup/ files and asks for no handoff.
func compileMentions(names []string) []mention {
	const edge, end = `(^|[^\w/-])`, `($|[^\w-])`
	var out []mention
	for _, name := range names {
		n := regexp.QuoteMeta(name)
		var pattern string
		if strings.Contains(name, "-") {
			pattern = `(^|[^\w-])` + n + end
		} else {
			pattern = edge + `/` + n + end + `|skills/` + n + `/SKILL\.md|` + edge + n + ` skill\b`
		}
		out = append(out, mention{name: name, re: regexp.MustCompile(`(?i)` + pattern)})
	}
	return out
}

// bashCall is what a Bash call ran of each fact the reader lifts from one,
// kept until the call's result says what happened. Its events come under
// the prompt the call was made in, which a prompt taken mid-turn may follow
// before the result arrives.
type bashCall struct {
	at             time.Time
	prompt         int
	commits        []commitCommand
	mentionsCommit bool
	envoy          roundCall
	createsPR      bool
	reads          []string // the skills whose file the call prints
	prints         string   // the one file the call prints and nothing else
}

// pointer is a prompt that asks for a file to be read and done: the file's
// text is the prompt. The session's own read of the file carries that text
// into the transcript, where it outlives the file.
type pointer struct {
	path    string
	at      time.Time
	prompt  int
	keys    []string // the snippets the file's text held
	settled bool     // a read gave the whole text, or a snippet in it
}

type slashCommand struct {
	at       time.Time
	name     string
	args     string
	promptID string
	pasted   []string // the snippets whose opening the command and its arguments hold
}

type prompt struct {
	at   time.Time
	text string
}

type decoder struct {
	snippets config.Snippets
	mentions []mention

	labelled map[string]bool // the skill names some label lists

	rec        *Record
	recognised int // rows that decode and carry a type

	lastAt     time.Time
	cwd        string
	skillCalls map[string]int       // Skill tool call id → its event
	reads      map[string]Event     // Read call id → the skill file it read
	pointed    map[string]string    // Read call id → the pointer's file it read
	pointers   []*pointer           // the pointer prompts, in order
	bash       map[string]*bashCall // Bash tool call id → what it ran
	rounds     rounds               // the envoy rounds, joined in rounds.go
	prs        map[string]int       // pull request URL → its event
	created    map[string]time.Time // URLs `gh pr create` returned → the call
	slash      *slashCommand        // a slash command waiting for its expansion
	marked     bool                 // a row said whose prompt it was or where it came from
	prompts    int                  // the human prompts read so far
	unsourced  []prompt             // prompts with no origin, used when no row is marked

	sig struct{ slash, tool, pr, compaction, human, pointer, commit, round Signal }
}

func newDecoder(rec *Record, snippets config.Snippets, mentions []mention) *decoder {
	labelled := map[string]bool{}
	for _, m := range mentions {
		labelled[m.name] = true
	}
	d := &decoder{
		snippets:   snippets,
		mentions:   mentions,
		labelled:   labelled,
		rec:        rec,
		skillCalls: map[string]int{},
		reads:      map[string]Event{},
		pointed:    map[string]string{},
		bash:       map[string]*bashCall{},
		prs:        map[string]int{},
		created:    map[string]time.Time{},
	}
	d.rounds = newRounds(rec, &d.sig.round, &d.prompts)
	return d
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
				d.recognised++
			case complete:
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

// add records an event under the prompt read last.
func (d *decoder) add(e Event) int {
	e.Prompt = d.prompts
	return d.keep(e)
}

// keep records an event under the prompt it already names: what a call's
// result settles came under the prompt the call was made in.
func (d *decoder) keep(e Event) int {
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
	case "attachment":
		return d.attachment(at, err == nil, r)
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

// attachment reads what the harness added to the conversation. One kind is
// the user's: a prompt typed while the session worked, which it took
// mid-turn. Claude Code records that as a queued_command attachment and
// never as a prompt row; one taken after the turn ended is a prompt row of
// its own. Every other kind is the harness's and is passed over.
func (d *decoder) attachment(at time.Time, dated bool, r row) bool {
	var kind struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(r.Attachment, &kind) != nil || kind.Type != "queued_command" {
		return true
	}
	var a struct {
		Prompt json.RawMessage `json:"prompt"`
		Origin *struct {
			Kind string `json:"kind"`
		} `json:"origin"`
		HumanTurn bool `json:"humanTurn"`
	}
	if json.Unmarshal(r.Attachment, &a) != nil || !dated {
		return false
	}
	text, _, ok := content(a.Prompt)
	if !ok {
		return false
	}
	if r.IsSidechain {
		return true
	}
	human := a.Origin != nil && a.Origin.Kind == "human"
	// humanTurn is the second trace, as promptSource is for a prompt row.
	if a.HumanTurn {
		d.sig.human.Second++
		if !human {
			d.sig.human.Missed++
		}
	}
	if !human {
		return true
	}
	d.marked = true
	d.lastAt, d.rec.LastAt = at, at
	// A command still waiting for its expansion has none: this prompt came
	// next.
	if d.slash != nil {
		d.unexpanded(d.slash)
		d.slash = nil
	}
	d.prompts++
	if strings.TrimSpace(text) != "" {
		d.human(at, text)
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
				Command    string `json:"command"`
				Background bool   `json:"run_in_background"`
			}
			if json.Unmarshal(b.Input, &in) != nil {
				ok = false
				continue
			}
			d.bash[b.ID] = d.bashCommand(at, in.Command, in.Background)
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
			// A Bash call that prints the file is the same read: bashCommand.
			if m := skillFile.FindStringSubmatch(in.FilePath); m != nil {
				d.reads[b.ID] = Event{At: at, Kind: Read, Name: m[1], Prompt: d.prompts}
			}
			if d.awaits(in.FilePath) {
				d.pointed[b.ID] = in.FilePath
			}
		}
	}
	return ok
}

// bashCommand records what a Bash call ran. Round events are added now, at
// the dispatch; commits and skill-file reads wait for the result, which says
// whether they happened. A call the tool ran in the background returns before
// anything in it has finished, so each of its commits is guarded and nothing
// it prints is in the result.
func (d *decoder) bashCommand(at time.Time, command string, background bool) *bashCall {
	call := &bashCall{at: at, prompt: d.prompts, mentionsCommit: strings.Contains(command, "commit")}
	call.envoy.mentions = envoyCall.MatchString(command)
	call.envoy.prompt = d.prompts
	// Text that does not parse yields no commands; a commit or round in it
	// shows as a miss in the second traces.
	cmds, _ := shell.Split(command, d.cwd)
	// A call's output is one file's text only when printing it is all the
	// call does.
	if len(cmds) == 1 && !cmds[0].Guarded && !background {
		if path, ok := filePrinted(cmds[0].Argv(), cmds[0]); ok && d.awaits(path) {
			call.prints = path
		}
	}
	for _, c := range cmds {
		argv := c.Argv()
		if commit, ok := commitOf(argv, c); ok {
			if commit.dir = shell.Resolve(c.Dir, commit.dir); commit.dir == d.cwd {
				commit.dir = ""
			}
			commit.guarded = c.Guarded || background
			call.commits = append(call.commits, commit)
		}
		if !c.Guarded && !background {
			for _, name := range skillsPrinted(argv, c) {
				if !slices.Contains(call.reads, name) {
					call.reads = append(call.reads, name)
				}
			}
		}
		d.rounds.command(&call.envoy, at, argv)
		if len(argv) >= 3 && argv[0] == "gh" && argv[1] == "pr" && argv[2] == "create" {
			call.createsPR = true
		}
	}
	return call
}

func (d *decoder) user(at time.Time, r row, text string, blocks []block) {
	if r.Origin != nil || r.PromptSource != "" {
		d.marked = true
	}
	results := 0
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results++
		}
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			// The row's own record of what the tool returned belongs to its
			// one result; beside several it names none of them.
			var meta json.RawMessage
			if results == 1 {
				meta = r.ToolUseResult
			}
			d.result(at, b, meta)
		}
	}
	waiting := d.slash
	d.slash = nil
	if results > 0 {
		// Only the row right after a command can be its expansion.
		if waiting != nil {
			d.unexpanded(waiting)
		}
		return
	}

	trimmed := strings.TrimSpace(text)
	expansion := strings.HasPrefix(trimmed, skillExpansion)
	follows := waiting != nil && r.IsMeta && !r.IsCompactSummary && waiting.promptID == r.PromptID
	if waiting != nil && !(follows && expansion) {
		d.unexpanded(waiting)
	}
	switch {
	case r.IsCompactSummary:
		d.sig.compaction.Second++
	case r.IsMeta:
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
		// expansion only proves that the command was a skill. A built-in
		// such as /init answers with a meta prompt of its own, which does not
		// open with the skill's line.
		if follows && expansion {
			// The paste comes first: it is the prompt, and the run is what
			// the prompt did. So do the other skills the arguments ask for.
			for _, key := range waiting.pasted {
				d.add(Event{At: waiting.at, Kind: Snippet, Name: key, Command: waiting.name})
			}
			if len(waiting.pasted) == 0 {
				d.argued(waiting, nil, waiting.name)
			}
			d.add(Event{At: waiting.at, Kind: Skill, Via: "slash", Name: waiting.name, Args: clip(waiting.args, argsMax)})
			d.sig.slash.Primary++
		}
	default:
		human := r.Origin != nil && r.Origin.Kind == "human"
		if human {
			d.prompts++
		}
		// The second trace takes a source the reader does not know as the
		// user's: should Claude Code mark typed prompts in a new way, check
		// reports them missed where the steps would lose them in silence.
		byUser := r.PromptSource != "" && r.PromptSource != "sdk" && r.PromptSource != "system"
		if byUser {
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
			// A snippet that opens with a slash command arrives as that
			// command when Claude Code does not wrap the paste, and its words
			// are then the command's name and arguments.
			d.slash = &slashCommand{at: at, name: m[1], args: args, promptID: r.PromptID,
				pasted: d.snippets.Pasted("/" + m[1] + " " + args)}
			return
		}
		if trimmed == "" {
			return
		}
		switch {
		case human:
			d.human(at, text)
		case r.Origin == nil:
			d.unsourced = append(d.unsourced, prompt{at, text})
		}
	}
}

// unexpanded handles a slash command that no expansion followed. That is a
// built-in or a mod command, which is not an event, unless it holds a
// snippet's opening or names a labelled skill: then the user asked for the
// skill and no load was seen, which is shown as the paste or as the words
// typed, like any other prompt.
func (d *decoder) unexpanded(c *slashCommand) {
	for _, key := range c.pasted {
		d.add(Event{At: c.at, Kind: Snippet, Name: key})
	}
	if len(c.pasted) > 0 {
		return
	}
	var names []string
	if name := bareSkill(c.name); d.labelled[name] {
		names = append(names, name)
	}
	d.argued(c, names, "")
}

// argued lifts the request a typed command makes: the labelled skills its
// arguments name, after the names already known to be asked for. ran is the
// command when it loaded its skill: the run is then the request under a
// label that lists that skill, and the arguments are one only under others.
// A paste typed as a command is not read for names, as a pasted prompt is
// not.
func (d *decoder) argued(c *slashCommand, names []string, ran string) {
	for _, n := range d.named(c.args) {
		if n != bareSkill(c.name) && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		d.mention(c.at, "/"+c.name+" "+c.args, names, ran)
	}
}

// mention adds the request a prompt's words make for the skills they name,
// said in the words' opening with Claude Code's paste tags left out.
func (d *decoder) mention(at time.Time, text string, names []string, command string) {
	words := strings.Fields(pastedTag.ReplaceAllString(text, " "))
	d.add(Event{At: at, Kind: Mention, Names: names, Text: clip(strings.Join(words, " "), openingWords), Command: command})
}

// named are the labelled skills a prompt's words name.
func (d *decoder) named(text string) []string {
	var names []string
	for _, m := range d.mentions {
		if m.re.MatchString(text) {
			names = append(names, m.name)
		}
	}
	return names
}

// human lifts what a prompt the user sent shows, typed or taken mid-turn. A
// pointer's words are the sender's sentence and a path, so they ask for
// nothing; its file's text is read when the session reads the file.
func (d *decoder) human(at time.Time, text string) {
	d.sig.human.Primary++
	t := strings.TrimSpace(text)
	if pointerLike.MatchString(t) {
		d.sig.pointer.Second++
	}
	if m := pointerPrompt.FindStringSubmatch(t); m != nil {
		d.pointers = append(d.pointers, &pointer{path: m[1], at: at, prompt: d.prompts})
		return
	}
	d.prompt(at, text)
}

// awaits reports whether a pointer's file has yet to be read whole.
func (d *decoder) awaits(path string) bool {
	return slices.ContainsFunc(d.pointers, func(p *pointer) bool { return p.path == path && !p.settled })
}

// readPointed matches a read of a pointer's file against the snippets. Each
// snippet the text holds is a paste dated at the pointer and under its
// prompt, however much later the read came; the same path sent twice before
// a read is two prompts that carried the same text. A read that gave part
// of the file and no snippet leaves the pointer to a later read.
func (d *decoder) readPointed(path, text string, whole bool) {
	keys := d.snippets.Pasted(text)
	for _, p := range d.pointers {
		if p.path != path || p.settled {
			continue
		}
		for _, key := range keys {
			if !slices.Contains(p.keys, key) {
				p.keys = append(p.keys, key)
				d.keep(Event{At: p.at, Kind: Snippet, Name: key, Via: "file", Prompt: p.prompt})
			}
		}
		if whole || len(p.keys) > 0 {
			p.settled = true
			d.sig.pointer.Primary++
		}
	}
}

// pointedText is the text a Read returned of a pointer's file, and whether
// it is the whole file. The model sees the lines numbered; the result's own
// record of the file holds them as written.
func pointedText(meta json.RawMessage, path string) (text string, whole, ok bool) {
	var r struct {
		File *struct {
			FilePath   string `json:"filePath"`
			Content    string `json:"content"`
			NumLines   int    `json:"numLines"`
			StartLine  int    `json:"startLine"`
			TotalLines int    `json:"totalLines"`
		} `json:"file"`
	}
	if json.Unmarshal(meta, &r) != nil || r.File == nil || r.File.FilePath != path {
		return "", false, false
	}
	f := r.File
	return f.Content, f.StartLine <= 1 && f.NumLines >= f.TotalLines, true
}

// prompt lifts what a human prompt shows: a pasted snippet, or failing that
// the labelled skills it names.
func (d *decoder) prompt(at time.Time, text string) {
	pasted := d.snippets.Pasted(text)
	for _, key := range pasted {
		d.add(Event{At: at, Kind: Snippet, Name: key})
	}
	if len(pasted) > 0 {
		return
	}
	if names := d.named(text); len(names) > 0 {
		d.mention(at, text, names, "")
	}
}

func (d *decoder) result(at time.Time, b block, meta json.RawMessage) {
	if idx, ok := d.skillCalls[b.ToolUseID]; ok {
		if b.IsError {
			d.rec.Events[idx].Failed = true
		}
		return
	}
	if path, ok := d.pointed[b.ToolUseID]; ok {
		delete(d.pointed, b.ToolUseID)
		if text, whole, ok := pointedText(meta, path); ok && !b.IsError {
			d.readPointed(path, text, whole)
		}
	}
	if read, ok := d.reads[b.ToolUseID]; ok {
		delete(d.reads, b.ToolUseID)
		if !b.IsError {
			d.keep(read)
		}
		return
	}
	call := d.bash[b.ToolUseID]
	if call == nil {
		return
	}
	delete(d.bash, b.ToolUseID)
	text, _, _ := content(b.Content)

	// cat prints no line of its own, and the output cannot stand in for one:
	// `cat -n` numbers a skill's opening lines and a long output is set aside
	// with only its start kept. So a read counts on the call's success, as a
	// Read call does, and a guarded one never counts.
	if !b.IsError {
		for _, name := range call.reads {
			d.keep(Event{At: call.at, Kind: Read, Name: name, Prompt: call.prompt})
		}
	}
	// A pointer's file printed by cat is the output itself, unless Claude
	// Code set it aside and kept only its start.
	if call.prints != "" && !b.IsError {
		d.readPointed(call.prints, text, !strings.HasPrefix(strings.TrimSpace(text), "<persisted-output>"))
	}
	d.committed(call, b.IsError, text)
	d.rounds.settle(&call.envoy, call.at, at, b.IsError, text)
	if call.createsPR && !b.IsError {
		for _, url := range pullURL.FindAllString(text, -1) {
			if _, seen := d.created[url]; !seen {
				d.created[url] = call.at
			}
		}
	}
}

// committed settles a call's commits with its result. A call that returned an
// error made no commit, unless git's own summary line is in the output: the
// commit succeeded and a later command failed. A guarded commit may have been
// skipped by a call that succeeded, so it counts on the summary line alone.
// Most commits run with -q and print none, which is why an unguarded one
// needs only the call's success.
func (d *decoder) committed(call *bashCall, failed bool, text string) {
	summary := gitSummary.MatchString(text)
	for _, c := range call.commits {
		if summary || !failed && !c.guarded {
			d.keep(Event{At: call.at, Kind: Commit, Text: c.subject, Amend: c.amend, Dir: c.dir, Prompt: call.prompt})
			d.sig.commit.Primary++
		}
	}
	if summary {
		d.sig.commit.Second++
		if len(call.commits) == 0 && call.mentionsCommit {
			d.sig.commit.Missed++
		}
	}
}

func (d *decoder) finish() {
	if d.slash != nil {
		d.unexpanded(d.slash)
	}
	// Transcripts from before Claude Code marked a prompt's origin or source:
	// take the plain text rows as the user's own. A session a program started
	// marks every prompt's source and no origin, and none of them is the
	// user's.
	if !d.marked {
		for _, p := range d.unsourced {
			t := strings.TrimSpace(p.text)
			if strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") || strings.HasPrefix(t, "/") {
				continue
			}
			d.prompt(p.at, p.text)
		}
	}
	for url, idx := range d.prs {
		_, d.rec.Events[idx].OpenedHere = d.created[url]
	}
	// Claude Code now and then writes no link row for a pull request it saw
	// created. The URL gh printed is the pull request; the missing row still
	// counts as a miss, since check watches the format.
	d.sig.pr.Primary, d.sig.pr.Second = len(d.prs), len(d.created)
	for _, url := range slices.Sorted(maps.Keys(d.created)) {
		if _, linked := d.prs[url]; linked {
			continue
		}
		m := pullURL.FindStringSubmatch(url)
		number, _ := strconv.Atoi(m[2])
		d.add(Event{At: d.created[url], Kind: PR, Repo: m[1], Number: number, URL: url, OpenedHere: true})
		d.sig.pr.Missed++
		d.sig.pr.Filled++
	}
	if extra := d.sig.compaction.Second - d.sig.compaction.Primary; extra > 0 {
		d.sig.compaction.Missed = extra
	}
	// A pointer no read gave whole, and a prompt worded like one that the
	// reader did not take for one, may each have carried a paste.
	d.sig.pointer.Missed = d.sig.pointer.Second - d.sig.pointer.Primary
	slices.SortStableFunc(d.rec.Events, func(a, b Event) int { return a.At.Compare(b.At) })

	d.rec.Signals = []Signal{
		d.sig.slash.as("skill, typed", "a slash command followed by its expansion / an expansion row with no tool call behind it"),
		d.sig.tool.as("skill, model call", "a Skill tool call / an expansion row that names a tool call"),
		d.sig.human.as("human prompt", "a prompt row or a prompt taken mid-turn with origin human / a row whose source is neither sdk nor system, or a prompt taken mid-turn marked as the user's turn"),
		d.sig.pointer.as("file pointer", "a pointer prompt a read of its file answered / a one-line prompt that opens \"Read <path>\""),
		d.sig.round.as("envoy round", "an envoy run or collect in command position / a result of an envoy call holding its job and status lines, or a call whose job neither its text nor its result names"),
		d.sig.commit.as("commit", "a git commit in command position / a result holding git's commit summary line"),
		d.sig.pr.as("pull request", "a pull-request link row / a URL returned by gh pr create"),
		d.sig.compaction.as("compaction", "a compaction boundary / a compaction summary row"),
	}
}

// as names a signal's fact and says what its two counts are.
func (s Signal) as(fact, note string) Signal {
	s.Fact, s.Note = fact, note
	return s
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}
