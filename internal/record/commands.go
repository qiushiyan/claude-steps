package record

import (
	"strings"

	"github.com/qiushiyan/claude-steps/internal/shell"
)

// This file reads a simple command's words as git, envoy or cat would: which
// commands are a commit, an envoy run, an envoy collect or a skill's file
// printed, and what each names. It knows no transcript row; internal/shell
// has already said what ran.

type commitCommand struct {
	subject string
	amend   bool
	dir     string // the argument of git -C
	guarded bool   // it may not have run although the call succeeded
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

// named reports whether a job argument is a name the text gives.
// internal/shell keeps the source text of a part it cannot read without
// running the shell: a variable set at run time, a command substitution.
func named(job string) bool {
	return !strings.ContainsAny(job, "$`")
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

// skillsPrinted returns the skills whose file a simple command prints whole
// into the call's output: the operands of `cat` that are a skill's file,
// read from the directory the command runs in. That is the model loading a
// skill without the Skill or the Read tool. A `cat` whose output a pipe or a
// file takes shows the model no file, and sed, head and grep show a passage
// of one, which is a lookup, so none of them is a read. A name the text does
// not spell out, a variable or a glob, names no skill.
func skillsPrinted(argv []string, c shell.Command) []string {
	if len(argv) < 2 || argv[0] != "cat" || c.Captured {
		return nil
	}
	var names []string
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		m := skillFile.FindStringSubmatch(shell.Resolve(c.Dir, arg))
		if m != nil && !strings.ContainsAny(m[1], "$`*?[{") {
			names = append(names, m[1])
		}
	}
	return names
}
