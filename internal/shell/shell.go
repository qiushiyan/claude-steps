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
//   - `cd`, an assignment and a function definition hold for the commands
//     after them in the same scope, and a subshell, a pipeline member, a
//     command substitution or a background job is a scope of its own;
//   - a variable assigned on a branch decided at run time holds inside the
//     branch, and reads as its source text after it;
//   - a command that runs only on a branch decided at run time is marked
//     Guarded.
//
// It does not expand globs, aliases or anything that needs the process
// environment.
package shell

import (
	"maps"
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
	// Guarded is set when the command may not have run although the text
	// as a whole succeeded: it stands after ||, in the body of an if, case
	// or loop, or in a background job. A command after && is not guarded: if
	// it is skipped, the && list fails.
	Guarded bool
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
	w := &walker{src: src}
	w.stmts(file.Stmts, &scope{dir: dir, vars: map[string]string{}, funcs: map[string]*syntax.Stmt{}}, input{})
	return w.out, nil
}

// A function may call itself; inlining stops at this depth.
const maxCallDepth = 4

type walker struct {
	src   string
	out   []Command
	depth int
	guard int // how many guarded branches enclose the statement walked
}

// scope is what a shell holds that a subshell gets a copy of: where it is,
// the variables the text has assigned, and the functions it has defined. A
// variable missing from vars has a value the text does not give.
type scope struct {
	dir   string
	vars  map[string]string
	funcs map[string]*syntax.Stmt
}

func (s *scope) fork() *scope {
	return &scope{dir: s.dir, vars: maps.Clone(s.vars), funcs: maps.Clone(s.funcs)}
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
		w.guard++
		defer func() { w.guard-- }()
	}
	// A redirection's word and an unquoted here-document expand before the
	// command runs.
	for _, r := range s.Redirs {
		if r.Word != nil {
			w.substitutions(r.Word, sc)
		}
		if r.Hdoc != nil {
			w.substitutions(r.Hdoc, sc)
		}
		switch {
		case r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc:
			in = input{set: true}
			if r.Hdoc != nil {
				in.body = w.text(sc, r.Hdoc, heredoc)
			}
		case r.Op == syntax.WordHdoc && r.Word != nil:
			in = input{w.text(sc, r.Word, unquoted), true}
		}
	}
	switch c := s.Cmd.(type) {
	case *syntax.CallExpr:
		w.call(c, sc, in)
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.Pipe, syntax.PipeAll:
			w.stmt(c.X, sc.fork(), in)
			w.stmt(c.Y, sc.fork(), in)
		case syntax.OrStmt:
			w.stmt(c.X, sc, in)
			w.guarded(sc, func(sc *scope) { w.stmt(c.Y, sc, in) })
		default:
			w.stmt(c.X, sc, in)
			w.stmt(c.Y, sc, in)
		}
	case *syntax.Subshell:
		w.stmts(c.Stmts, sc.fork(), in)
	case *syntax.Block:
		w.stmts(c.Stmts, sc, in)
	case *syntax.IfClause:
		// The first condition always runs; every other part is a branch.
		w.stmts(c.Cond, sc, in)
		branches := []func(*scope){func(sc *scope) { w.stmts(c.Then, sc, in) }}
		for clause := c.Else; clause != nil; clause = clause.Else {
			branches = append(branches, func(sc *scope) {
				w.stmts(clause.Cond, sc, in)
				w.stmts(clause.Then, sc, in)
			})
		}
		w.guarded(sc, branches...)
	case *syntax.WhileClause:
		w.stmts(c.Cond, sc, in)
		w.guarded(sc, func(sc *scope) { w.stmts(c.Do, sc, in) })
	case *syntax.ForClause:
		w.substitutions(c.Loop, sc)
		w.guarded(sc, func(sc *scope) { w.stmts(c.Do, sc, in) })
	case *syntax.TestClause:
		w.substitutions(c.X, sc)
	case *syntax.ArithmCmd:
		w.substitutions(c.X, sc)
	case *syntax.LetClause:
		for _, x := range c.Exprs {
			w.substitutions(x, sc)
		}
	case *syntax.CaseClause:
		w.substitutions(c.Word, sc)
		var branches []func(*scope)
		for _, item := range c.Items {
			branches = append(branches, func(sc *scope) { w.stmts(item.Stmts, sc, in) })
		}
		w.guarded(sc, branches...)
	case *syntax.TimeClause:
		w.stmt(c.Stmt, sc, in)
	case *syntax.CoprocClause:
		w.stmt(c.Stmt, sc.fork(), in)
	case *syntax.FuncDecl:
		body, rest := funcBody(c.Body)
		if c.Name != nil {
			sc.funcs[c.Name.Value] = body
		}
		for _, n := range c.Names {
			sc.funcs[n.Value] = body
		}
		w.stmts(rest, sc, in)
	case *syntax.DeclClause:
		for _, a := range c.Args {
			if a.Value != nil {
				w.substitutions(a.Value, sc)
			}
		}
		w.assign(sc, c.Args)
	}
}

// guarded walks the branches of a choice made at run time: the arms of an if
// or a case, a loop's body, the right of an ||. Each starts from the
// variables set before the choice, and what it assigns holds inside it.
// After the choice the shell may hold any branch's value or the earlier one,
// so a variable a branch changed has a value the text does not give. A `cd`
// in a branch still moves what follows, as it always has.
func (w *walker) guarded(sc *scope, branches ...func(*scope)) {
	before := maps.Clone(sc.vars)
	w.guard++
	for _, walk := range branches {
		branch := &scope{dir: sc.dir, vars: maps.Clone(before), funcs: sc.funcs}
		walk(branch)
		sc.dir = branch.dir
		for name, value := range before {
			if now, ok := branch.vars[name]; !ok || now != value {
				delete(sc.vars, name)
			}
		}
	}
	w.guard--
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
		w.assign(sc, c.Assigns)
		return
	}

	words := make([]string, 0, len(c.Assigns)+len(c.Args))
	for _, a := range c.Assigns {
		if a.Name != nil && a.Value != nil {
			words = append(words, a.Name.Value+"="+w.text(sc, a.Value, unquoted))
		}
	}
	first := len(words)
	for _, word := range c.Args {
		words = append(words, w.text(sc, word, unquoted))
	}
	name := words[first]

	if body, ok := sc.funcs[name]; ok && w.depth < maxCallDepth {
		w.depth++
		w.stmt(body, sc, in)
		w.depth--
		return
	}
	// zsh's precommand modifiers run the builtin in this shell. `command cd`
	// would too in bash; zsh's command runs an external program, which moves
	// nothing.
	at := first
	for at+1 < len(words) && (words[at] == "noglob" || words[at] == "nocorrect" || words[at] == "builtin") {
		at++
	}
	// `cd -` goes back to a directory the text does not name.
	if words[at] == "cd" && len(words) == at+2 && words[at+1] != "-" {
		sc.dir = Resolve(sc.dir, words[at+1])
	}
	w.out = append(w.out, Command{Words: words, Stdin: in.body, HasStdin: in.set, Dir: sc.dir, Guarded: w.guard > 0})
}

// assign records variables whose values the text sets. The caller has
// already walked the values' command substitutions.
func (w *walker) assign(sc *scope, list []*syntax.Assign) {
	for _, a := range list {
		if a.Name == nil || a.Array != nil || a.Index != nil {
			continue
		}
		value := ""
		if a.Value != nil {
			value = w.text(sc, a.Value, unquoted)
		}
		if a.Append {
			value = sc.vars[a.Name.Value] + value
		}
		sc.vars[a.Name.Value] = value
	}
}

// substitutions walks the commands inside the command and process
// substitutions of a word, a test or a loop's word list. Each runs in a
// scope of its own.
func (w *walker) substitutions(n syntax.Node, sc *scope) {
	syntax.Walk(n, func(n syntax.Node) bool {
		switch s := n.(type) {
		case *syntax.CmdSubst:
			w.stmts(s.Stmts, sc.fork(), input{})
			return false
		case *syntax.ProcSubst:
			w.stmts(s.Stmts, sc.fork(), input{})
			return false
		}
		return true
	})
}

// Resolve returns where a directory argument points for a command that runs
// in dir, as cd and git -C read one: an absolute path or one under "~" stands
// alone, and an empty dir leaves a relative path relative.
func Resolve(dir, arg string) string {
	switch {
	case arg == "":
		return dir
	case path.IsAbs(arg) || strings.HasPrefix(arg, "~") || dir == "":
		return path.Clean(arg)
	}
	return path.Join(dir, arg)
}

type quoting int

const (
	unquoted quoting = iota
	doubleQuoted
	heredoc
)

// text reads a word as the shell would see it, keeping the source text of
// anything that needs the shell to run.
func (w *walker) text(sc *scope, word *syntax.Word, q quoting) string {
	var b strings.Builder
	for _, part := range word.Parts {
		w.part(sc, &b, part, q)
	}
	return b.String()
}

func (w *walker) part(sc *scope, b *strings.Builder, part syntax.WordPart, q quoting) {
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
			w.part(sc, b, inner, doubleQuoted)
		}
	case *syntax.ParamExp:
		if v, ok := sc.vars[plainParam(p)]; ok {
			b.WriteString(v)
			return
		}
		b.WriteString(w.source(p))
	case *syntax.CmdSubst:
		if body, ok := w.catHeredoc(sc, p); ok {
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
func (w *walker) catHeredoc(sc *scope, cs *syntax.CmdSubst) (string, bool) {
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
			return w.text(sc, r.Hdoc, heredoc), true
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
