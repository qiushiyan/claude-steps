// Package shell splits the text of a Bash tool call into simple commands, so
// a caller can ask what ran in command position without matching substrings
// of quoted text, here-documents or commit messages.
//
// It reads enough shell to keep quoting, command substitution and
// here-documents from leaking into command boundaries. It does not expand
// variables, globs or aliases.
package shell

import (
	"path"
	"strings"
)

// Command is one simple command: its words with quoting removed.
type Command struct {
	Words []string
	// Heredoc is the body of the first here-document or here-string attached
	// to the command; HasHeredoc tells an empty body from none.
	Heredoc    string
	HasHeredoc bool

	// defines names the function this command's line declares; inBody marks
	// a command inside a function body. Neither runs when it is read.
	defines string
	inBody  bool
}

// Argv returns the words after leading variable assignments and wrapper
// commands, with the program reduced to its base name.
func (c Command) Argv() []string {
	words := c.Words
	for len(words) > 0 {
		w := words[0]
		if isAssignment(w) || w == "command" || w == "exec" || w == "noglob" || w == "nocorrect" || w == "env" {
			words = words[1:]
			continue
		}
		break
	}
	if len(words) == 0 {
		return nil
	}
	argv := append([]string{path.Base(words[0])}, words[1:]...)
	return argv
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		ch := w[i]
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}

// Split returns every simple command src runs, in source order, including the
// commands inside command substitutions and subshells.
//
// Two indirections are followed, because a call that commits or dispatches
// through them is otherwise invisible. A function defined in src runs its
// body where it is called. A word that is one variable assigned in src
// ("envoy run $JOB") is replaced by that value, as a single word: the Bash
// tool runs zsh here, which does not split an unquoted variable.
func Split(src string) []Command {
	p := &parser{src: src, funcs: map[string][]int{}}
	p.list(top)

	vars := map[string]string{}
	var out []Command
	for _, c := range p.out {
		if c.defines != "" || c.inBody || len(c.Words) == 0 {
			continue
		}
		c = substitute(c, vars)
		assignments := true
		for _, w := range c.Words {
			assignments = assignments && isAssignment(w)
		}
		if assignments {
			for _, w := range c.Words {
				name, value, _ := strings.Cut(w, "=")
				vars[name] = value
			}
		}
		body, isCall := p.funcs[c.Words[0]]
		if !isCall {
			out = append(out, c)
			continue
		}
		// The call's here-document is the standard input of the body.
		for _, i := range body {
			b := substitute(p.out[i], vars)
			b.inBody = false
			if c.HasHeredoc && !b.HasHeredoc {
				b.Heredoc, b.HasHeredoc = c.Heredoc, true
			}
			out = append(out, b)
		}
	}
	return out
}

// substitute replaces each word that is exactly "$NAME" or "${NAME}" with
// the value assigned to NAME earlier in the same source.
func substitute(c Command, vars map[string]string) Command {
	var words []string
	for i, w := range c.Words {
		name, ok := strings.CutPrefix(w, "$")
		if inner, braced := strings.CutPrefix(name, "{"); ok && braced {
			name, ok = strings.CutSuffix(inner, "}")
		}
		value, set := vars[name]
		if !ok || !set {
			continue
		}
		if words == nil {
			words = append([]string(nil), c.Words...)
		}
		words[i] = value
	}
	if words != nil {
		c.Words = words
	}
	return c
}

// What ends a list of commands.
const (
	top   = iota // the end of input
	paren        // the ")" of a substitution or subshell
	brace        // the "}" of a function body
)

// Words reserved by the shell that may stand before a command.
var leading = map[string]bool{
	"{": true, "}": true, "!": true, "if": true, "then": true, "else": true, "elif": true,
	"do": true, "while": true, "until": true, "time": true,
}

type heredoc struct {
	delim     string
	stripTabs bool
	cmd       int // index into parser.out, -1 when the command was dropped
}

type parser struct {
	src     string
	pos     int
	out     []Command
	pending []heredoc
	funcs   map[string][]int // function name → the commands of its body, in out
}

// list reads commands until what closes it: the end of input, a ")" or a "}".
func (p *parser) list(until int) {
	cur := -1 // index of the command being built in p.out
	var word strings.Builder
	inWord := false
	dropNext := false // the next word is a redirection target

	closed := false // the "}" that ends a function body was read
	endWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		word.Reset()
		inWord = false
		if dropNext {
			dropNext = false
			return
		}
		if cur < 0 {
			if w == "}" && until == brace {
				closed = true
				return
			}
			if leading[w] {
				return
			}
			p.out = append(p.out, Command{})
			cur = len(p.out) - 1
		}
		p.out[cur].Words = append(p.out[cur].Words, w)
	}
	endCommand := func() {
		endWord()
		dropNext = false
		cur = -1
	}

	for p.pos < len(p.src) && !closed {
		ch := p.src[p.pos]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r':
			endWord()
			p.pos++
		case ch == '\n':
			endCommand()
			p.pos++
			p.readHeredocs()
		case ch == '#' && !inWord:
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case ch == '\\':
			if p.pos+1 < len(p.src) {
				if p.src[p.pos+1] != '\n' {
					word.WriteByte(p.src[p.pos+1])
					inWord = true
				}
				p.pos += 2
			} else {
				p.pos++
			}
		case ch == '\'':
			end := strings.IndexByte(p.src[p.pos+1:], '\'')
			if end < 0 {
				word.WriteString(p.src[p.pos+1:])
				p.pos = len(p.src)
			} else {
				word.WriteString(p.src[p.pos+1 : p.pos+1+end])
				p.pos += end + 2
			}
			inWord = true
		case ch == '"':
			p.pos++
			p.doubleQuoted(&word)
			inWord = true
		case ch == '`':
			end := strings.IndexByte(p.src[p.pos+1:], '`')
			if end < 0 {
				end = len(p.src) - p.pos - 1
			}
			word.WriteString(p.src[p.pos:min(p.pos+end+2, len(p.src))])
			p.pos = min(p.pos+end+2, len(p.src))
			inWord = true
		case ch == '$' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '(':
			word.WriteString(p.substitution())
			inWord = true
		case ch == ';' || ch == '|':
			endCommand()
			p.pos++
		case ch == '&':
			// "&>" and "&>>" redirect; any other "&" ends the command.
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '>' {
				endWord()
				p.pos++
				continue
			}
			endCommand()
			p.pos++
		case ch == '(':
			if inWord && cur < 0 && p.functionBody(word.String()) {
				word.Reset()
				inWord = false
				continue
			}
			endCommand()
			p.pos++
			p.list(paren)
		case ch == ')':
			endCommand()
			p.pos++
			if until == paren {
				return
			}
		case ch == '<' || ch == '>':
			// A word of digits just before the operator is its descriptor.
			if inWord && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			endWord()
			if ch == '<' && strings.HasPrefix(p.src[p.pos:], "<<<") {
				p.pos += 3
				p.hereString(cur)
				continue
			}
			if ch == '<' && strings.HasPrefix(p.src[p.pos:], "<<") {
				p.pos += 2
				p.heredocStart(cur)
				continue
			}
			for p.pos < len(p.src) && (p.src[p.pos] == '<' || p.src[p.pos] == '>' || p.src[p.pos] == '|') {
				p.pos++
			}
			if p.pos < len(p.src) && p.src[p.pos] == '&' {
				// "2>&1": the descriptor after "&" is part of the operator.
				p.pos++
				for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '-') {
					p.pos++
				}
				continue
			}
			dropNext = true
		default:
			word.WriteByte(ch)
			inWord = true
			p.pos++
		}
	}
	endCommand()
}

// functionBody reads "() { … }" after a function's name, from the "(". It
// keeps the body's commands in out, marked so that they run only where the
// function is called. It reports false, having read nothing, when the text is
// not a function definition.
func (p *parser) functionBody(name string) bool {
	rest := p.src[p.pos+1:]
	afterParen := strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(afterParen, ")") {
		return false
	}
	afterBrace := strings.TrimLeft(afterParen[1:], " \t\n")
	if !strings.HasPrefix(afterBrace, "{") || !isAssignment(strings.ReplaceAll(name, "-", "_")+"=") {
		return false
	}
	p.pos = len(p.src) - len(afterBrace) + 1
	p.out = append(p.out, Command{Words: []string{name}, defines: name})
	first := len(p.out)
	p.list(brace)
	var body []int
	for i := first; i < len(p.out); i++ {
		p.out[i].inBody = true
		body = append(body, i)
	}
	p.funcs[name] = body
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// doubleQuoted reads up to the closing quote, appending the text to word.
func (p *parser) doubleQuoted(word *strings.Builder) {
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		switch {
		case ch == '"':
			p.pos++
			return
		case ch == '\\' && p.pos+1 < len(p.src):
			next := p.src[p.pos+1]
			if next == '"' || next == '\\' || next == '$' || next == '`' {
				word.WriteByte(next)
			} else if next != '\n' {
				word.WriteByte(ch)
				word.WriteByte(next)
			}
			p.pos += 2
		case ch == '$' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '(':
			word.WriteString(p.substitution())
		default:
			word.WriteByte(ch)
			p.pos++
		}
	}
}

// substitution reads "$( … )" from its "$". It returns the body of the
// here-document when the substitution is exactly `cat <<DELIM … DELIM`, the
// form a commit message is passed in, and the raw text otherwise.
func (p *parser) substitution() string {
	start := p.pos
	p.pos += 2
	first := len(p.out)
	p.list(paren)
	raw := p.src[start:p.pos]
	if len(p.out) == first+1 {
		c := p.out[first]
		if len(c.Words) == 1 && c.Words[0] == "cat" && c.HasHeredoc {
			return c.Heredoc
		}
	}
	return raw
}

// heredocStart reads the delimiter after "<<" and queues the body, which
// starts after the next newline.
func (p *parser) heredocStart(cmd int) {
	strip := false
	if p.pos < len(p.src) && p.src[p.pos] == '-' {
		strip = true
		p.pos++
	}
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	var delim strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '\'' || ch == '"' {
			end := strings.IndexByte(p.src[p.pos+1:], ch)
			if end < 0 {
				p.pos = len(p.src)
				break
			}
			delim.WriteString(p.src[p.pos+1 : p.pos+1+end])
			p.pos += end + 2
			continue
		}
		if ch == '\\' && p.pos+1 < len(p.src) {
			delim.WriteByte(p.src[p.pos+1])
			p.pos += 2
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == ';' || ch == '&' || ch == '|' || ch == ')' || ch == '<' || ch == '>' {
			break
		}
		delim.WriteByte(ch)
		p.pos++
	}
	p.pending = append(p.pending, heredoc{delim: delim.String(), stripTabs: strip, cmd: cmd})
}

// hereString reads the word after "<<<" as the command's here-document.
func (p *parser) hereString(cmd int) {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	var word strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '\'' {
			end := strings.IndexByte(p.src[p.pos+1:], '\'')
			if end < 0 {
				end = len(p.src) - p.pos - 1
			}
			word.WriteString(p.src[p.pos+1 : p.pos+1+end])
			p.pos = min(p.pos+end+2, len(p.src))
			continue
		}
		if ch == '"' {
			p.pos++
			p.doubleQuoted(&word)
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == ';' || ch == '&' || ch == '|' || ch == ')' {
			break
		}
		word.WriteByte(ch)
		p.pos++
	}
	p.attach(cmd, word.String())
}

// readHeredocs consumes the bodies queued on the line that just ended.
func (p *parser) readHeredocs() {
	pending := p.pending
	p.pending = nil
	for _, h := range pending {
		var body strings.Builder
		closed := false
		for p.pos < len(p.src) && !closed {
			end := strings.IndexByte(p.src[p.pos:], '\n')
			var line string
			if end < 0 {
				line = p.src[p.pos:]
				p.pos = len(p.src)
			} else {
				line = p.src[p.pos : p.pos+end]
				p.pos += end + 1
			}
			probe := strings.TrimSuffix(line, "\r")
			if h.stripTabs {
				probe = strings.TrimLeft(probe, "\t")
			}
			if probe == h.delim {
				closed = true
				break
			}
			body.WriteString(line)
			body.WriteByte('\n')
		}
		p.attach(h.cmd, body.String())
	}
}

func (p *parser) attach(cmd int, body string) {
	if cmd < 0 || cmd >= len(p.out) || p.out[cmd].HasHeredoc {
		return
	}
	p.out[cmd].Heredoc = body
	p.out[cmd].HasHeredoc = true
}
