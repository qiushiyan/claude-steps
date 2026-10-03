// Package shell reads the text of a Bash tool call and returns the simple
// commands it runs, each with its words, its standard input and the
// directory it runs in. A caller asks what ran in command position without
// matching substrings of quoted text, here-documents or commit messages, and
// without knowing shell scoping.
//
// It parses with mvdan.cc/sh and follows three things a commit or a dispatch
// is otherwise hidden behind:
//
//   - a function defined in the text runs its body where it is called, with
//     the call's here-document as the body's input;
//   - a word that is one variable assigned earlier in the text ("envoy run
//     $JOB") reads as that value, as one word: the Bash tool runs zsh here,
//     which does not split an unquoted variable, so a variable holding a
//     whole command does not run it;
//   - `cd` moves the commands after it in the same scope, and a subshell, a
//     pipeline member, a command substitution or a background job is a scope
//     of its own.
//
// It does not expand globs, aliases or anything that needs the process
// environment.
package shell

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Command is one simple command.
type Command struct {
	// Words are the command's words with quoting removed. A part that cannot
	// be read without running the shell (an unknown variable, a command
	// substitution) keeps its source text.
	Words []string
	// Stdin is the body of the here-document or here-string the command
	// reads, its own or the one its enclosing function call was given.
	Stdin    string
	HasStdin bool
	// Dir is where the command runs: the directory Split was given, moved by
	// the cd commands before it in its scope.
	Dir string
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
	return append([]string{path.Base(words[0])}, words[1:]...)
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	return eq > 0 && isName(w[:eq])
}

func isName(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9') {
			return false
		}
	}
	return s != ""
}

// Split returns the commands src runs, in source order, the commands inside
// command substitutions included. dir is where the call starts; an empty dir
// leaves relative directories relative. Text that parses neither as bash nor
// as zsh yields an error and no commands.
func Split(src, dir string) ([]Command, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		var zerr error
		if file, zerr = syntax.NewParser(syntax.Variant(syntax.LangZsh)).Parse(strings.NewReader(src), ""); zerr != nil {
			return nil, err
		}
	}
	w := &walker{src: src, funcs: map[string]*syntax.Stmt{}, vars: map[string]string{}}
	w.stmts(file.Stmts, &scope{dir: dir}, input{})
	return w.out, nil
}

// A function may call itself; inlining stops at this depth.
const maxCallDepth = 4

type walker struct {
	src   string
	out   []Command
	funcs map[string]*syntax.Stmt
	vars  map[string]string
	depth int
}

type scope struct {
	dir string
}

func (s *scope) fork() *scope {
	c := *s
	return &c
}

// input is the standard input a statement inherits from a function call.
type input struct {
	body string
	set  bool
}

func (w *walker) stmts(list []*syntax.Stmt, sc *scope, in input) {
	for _, s := range list {
		w.stmt(s, sc, in)
	}
}

func (w *walker) stmt(s *syntax.Stmt, sc *scope, in input) {
	if s == nil || s.Cmd == nil {
		return
	}
	if s.Background || s.Coprocess {
		sc = sc.fork()
	}
	for _, r := range s.Redirs {
		switch {
		case r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc:
			in = input{set: true}
			if r.Hdoc != nil {
				in.body = w.text(r.Hdoc, heredoc)
			}
		case r.Op == syntax.WordHdoc && r.Word != nil:
			in = input{w.text(r.Word, unquoted), true}
		}
	}
	switch c := s.Cmd.(type) {
	case *syntax.CallExpr:
		w.call(c, sc, in)
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			w.stmt(c.X, sc.fork(), in)
			w.stmt(c.Y, sc.fork(), in)
		} else {
			w.stmt(c.X, sc, in)
			w.stmt(c.Y, sc, in)
		}
	case *syntax.Subshell:
		w.stmts(c.Stmts, sc.fork(), in)
	case *syntax.Block:
		w.stmts(c.Stmts, sc, in)
	case *syntax.IfClause:
		for clause := c; clause != nil; clause = clause.Else {
			w.stmts(clause.Cond, sc, in)
			w.stmts(clause.Then, sc, in)
		}
	case *syntax.WhileClause:
		w.stmts(c.Cond, sc, in)
		w.stmts(c.Do, sc, in)
	case *syntax.ForClause:
		w.stmts(c.Do, sc, in)
	case *syntax.CaseClause:
		for _, item := range c.Items {
			w.stmts(item.Stmts, sc, in)
		}
	case *syntax.TimeClause:
		w.stmt(c.Stmt, sc, in)
	case *syntax.CoprocClause:
		w.stmt(c.Stmt, sc.fork(), in)
	case *syntax.FuncDecl:
		body, rest := funcBody(c.Body)
		if c.Name != nil {
			w.funcs[c.Name.Value] = body
		}
		for _, n := range c.Names {
			w.funcs[n.Value] = body
		}
		w.stmts(rest, sc, in)
	case *syntax.DeclClause:
		for _, a := range c.Args {
			if a.Value != nil {
				w.substitutions(a.Value, sc)
			}
		}
		w.assign(c.Args)
	}
}

// funcBody returns a function's body and the statements chained after the
// definition. In `f() { … } && b`, bash runs b once f is defined; the parser
// hands back `{ … } && b` as the body, so the leftmost statement of the chain
// is the body and the rest run where the definition stands.
func funcBody(s *syntax.Stmt) (*syntax.Stmt, []*syntax.Stmt) {
	b, ok := s.Cmd.(*syntax.BinaryCmd)
	if !ok || len(s.Redirs) > 0 {
		return s, nil
	}
	body, rest := funcBody(b.X)
	return body, append(rest, b.Y)
}

func (w *walker) call(c *syntax.CallExpr, sc *scope, in input) {
	// A command substitution runs, in a scope of its own, before the
	// command whose word holds it.
	for _, a := range c.Assigns {
		if a.Value != nil {
			w.substitutions(a.Value, sc)
		}
	}
	for _, word := range c.Args {
		w.substitutions(word, sc)
	}
	if len(c.Args) == 0 {
		w.assign(c.Assigns)
		return
	}

	words := make([]string, 0, len(c.Assigns)+len(c.Args))
	for _, a := range c.Assigns {
		if a.Name != nil && a.Value != nil {
			words = append(words, a.Name.Value+"="+w.text(a.Value, unquoted))
		}
	}
	first := len(words)
	for _, word := range c.Args {
		words = append(words, w.text(word, unquoted))
	}
	name := words[first]

	if body, ok := w.funcs[name]; ok && w.depth < maxCallDepth {
		w.depth++
		w.stmt(body, sc, in)
		w.depth--
		return
	}
	if name == "cd" && len(words) == first+2 {
		sc.dir = join(sc.dir, words[first+1])
	}
	w.out = append(w.out, Command{Words: words, Stdin: in.body, HasStdin: in.set, Dir: sc.dir})
}

// assign records variables whose values the text sets. The caller has
// already walked the values' command substitutions.
func (w *walker) assign(list []*syntax.Assign) {
	for _, a := range list {
		if a.Name == nil || a.Array != nil || a.Index != nil {
			continue
		}
		value := ""
		if a.Value != nil {
			value = w.text(a.Value, unquoted)
		}
		if a.Append {
			value = w.vars[a.Name.Value] + value
		}
		w.vars[a.Name.Value] = value
	}
}

// substitutions walks the commands inside a word's command substitutions.
func (w *walker) substitutions(word *syntax.Word, sc *scope) {
	syntax.Walk(word, func(n syntax.Node) bool {
		if cs, ok := n.(*syntax.CmdSubst); ok {
			w.stmts(cs.Stmts, sc.fork(), input{})
			return false
		}
		return true
	})
}

// join moves dir by a cd argument.
func join(dir, to string) string {
	switch {
	case to == "" || to == "-":
		return dir
	case path.IsAbs(to) || strings.HasPrefix(to, "~"):
		return path.Clean(to)
	case dir == "":
		return path.Clean(to)
	}
	return path.Join(dir, to)
}

type quoting int

const (
	unquoted quoting = iota
	doubleQuoted
	heredoc
)

// text reads a word as the shell would see it, keeping the source text of
// anything that needs the shell to run.
func (w *walker) text(word *syntax.Word, q quoting) string {
	var b strings.Builder
	for _, part := range word.Parts {
		w.part(&b, part, q)
	}
	return b.String()
}

func (w *walker) part(b *strings.Builder, part syntax.WordPart, q quoting) {
	switch p := part.(type) {
	case *syntax.Lit:
		b.WriteString(unescape(p.Value, q))
	case *syntax.SglQuoted:
		if p.Dollar {
			b.WriteString(w.source(p))
			return
		}
		b.WriteString(p.Value)
	case *syntax.DblQuoted:
		for _, inner := range p.Parts {
			w.part(b, inner, doubleQuoted)
		}
	case *syntax.ParamExp:
		if v, ok := w.vars[plainParam(p)]; ok {
			b.WriteString(v)
			return
		}
		b.WriteString(w.source(p))
	case *syntax.CmdSubst:
		if body, ok := w.catHeredoc(p); ok {
			b.WriteString(body)
			return
		}
		b.WriteString(w.source(p))
	default:
		b.WriteString(w.source(part))
	}
}

// catHeredoc reads `$(cat <<EOF … EOF)`, the form a commit message is passed
// in, as the here-document's body.
func (w *walker) catHeredoc(cs *syntax.CmdSubst) (string, bool) {
	if len(cs.Stmts) != 1 {
		return "", false
	}
	s := cs.Stmts[0]
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 1 || call.Args[0].Lit() != "cat" {
		return "", false
	}
	for _, r := range s.Redirs {
		if (r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc) && r.Hdoc != nil {
			return w.text(r.Hdoc, heredoc), true
		}
	}
	return "", false
}

// plainParam returns the name of "$NAME" or "${NAME}", or "" for any
// expansion that does more than read the value.
func plainParam(p *syntax.ParamExp) string {
	if p.Param == nil || p.Excl || p.Length || p.Width || p.IsSet || p.Index != nil || p.Slice != nil ||
		p.Repl != nil || p.Exp != nil || p.Names != 0 || p.Flags != nil || p.NestedParam != nil || len(p.Modifiers) > 0 {
		return ""
	}
	return p.Param.Value
}

func (w *walker) source(n syntax.Node) string {
	start, end := int(n.Pos().Offset()), int(n.End().Offset())
	if start < 0 || end > len(w.src) || start > end {
		return ""
	}
	return w.src[start:end]
}

// unescape removes the backslashes the shell removes from a literal. A
// here-document body is kept as written.
func unescape(s string, q quoting) string {
	if q == heredoc || !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		next := s[i+1]
		switch {
		case next == '\n':
		case q == unquoted:
			b.WriteByte(next)
		case next == '"' || next == '\\' || next == '$' || next == '`':
			b.WriteByte(next)
		default:
			b.WriteByte('\\')
			b.WriteByte(next)
		}
		i++
	}
	return b.String()
}
