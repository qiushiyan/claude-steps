package shell

import (
	"reflect"
	"strings"
	"testing"
)

func split(t *testing.T, src string) []Command {
	t.Helper()
	cmds, err := Split(src, "/work/app")
	if err != nil {
		t.Fatalf("Split(%q): %v", src, err)
	}
	return cmds
}

func argvs(t *testing.T, src string) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range split(t, src) {
		out = append(out, c.Argv())
	}
	return out
}

func TestSplitFindsCommandsInCommandPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want [][]string
	}{
		{"chain", `git add -A && git commit -q -m "one two"`, [][]string{{"git", "add", "-A"}, {"git", "commit", "-q", "-m", "one two"}}},
		{"assignment and path", `GIT_EDITOR=true /usr/bin/git -C ~/x commit -m s`, [][]string{{"git", "-C", "~/x", "commit", "-m", "s"}}},
		{"redirects", `envoy collect review-r1 2>&1 | head -20 > out.txt`, [][]string{{"envoy", "collect", "review-r1"}, {"head", "-20"}}},
		{"quoted text is not a command", `echo "then git commit -m x; envoy run y" ; ls`, [][]string{{"echo", "then git commit -m x; envoy run y"}, {"ls"}}},
		{"comment", "ls # git commit -m x\npwd", [][]string{{"ls"}, {"pwd"}}},
		{"subshell and keywords", `if true; then (cd x && git commit -m s); fi`, [][]string{{"true"}, {"cd", "x"}, {"git", "commit", "-m", "s"}}},
		{"substitution holds commands", `S=$(git rev-parse HEAD) && echo "$S"`, [][]string{{"git", "rev-parse", "HEAD"}, {"echo", "$(git rev-parse HEAD)"}}},
		{"background", `envoy run consult-r1 --with codex & wait`, [][]string{{"envoy", "run", "consult-r1", "--with", "codex"}, {"wait"}}},
		{"escapes", `git commit -m it\'s\ done`, [][]string{{"git", "commit", "-m", "it's done"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvs(t, tc.src); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Split(%q)\n got %q\nwant %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestHeredocMessageIsOneWord(t *testing.T) {
	src := "git add f && git commit -q -m \"$(cat <<'EOF'\nfix: the \"quoted\" thing; git commit && more\n\nbody \\n line\nEOF\n)\" && git log -1"
	cmds := split(t, src)
	var commit []string
	for _, c := range cmds {
		if a := c.Argv(); len(a) > 1 && a[0] == "git" && a[1] == "commit" {
			commit = a
		}
	}
	want := []string{"git", "commit", "-q", "-m", "fix: the \"quoted\" thing; git commit && more\n\nbody \\n line\n"}
	if !reflect.DeepEqual(commit, want) {
		t.Fatalf("commit argv\n got %q\nwant %q", commit, want)
	}
	if last := cmds[len(cmds)-1].Argv(); !reflect.DeepEqual(last, []string{"git", "log", "-1"}) {
		t.Errorf("command after the here-document: got %q", last)
	}
}

func TestHeredocOnStandardInput(t *testing.T) {
	cmds := split(t, "git commit -F - <<'MSG'\nsubject here\n\nbody\nMSG\necho done")
	if len(cmds) != 2 || !cmds[0].HasStdin || cmds[0].Stdin != "subject here\n\nbody\n" || cmds[1].HasStdin {
		t.Fatalf("got %+v", cmds)
	}
	here := split(t, `git commit -F - <<< "from a here-string"`)
	if len(here) != 1 || here[0].Stdin != "from a here-string" {
		t.Errorf("here-string: %+v", here)
	}
}

func TestFunctionRunsWhereItIsCalled(t *testing.T) {
	src := "git reset -q && commit() { git -c core.hooksPath=.githooks commit -q -F -; } && \\\ngit add a.ts && commit <<'EOF'\nfix: first\n\nbody\nEOF\ngit add b.ts && commit <<'EOF'\nfix: second\nEOF"
	var commits []Command
	for _, c := range split(t, src) {
		a := c.Argv()
		if len(a) > 0 && a[0] == "commit" {
			t.Errorf("the function's name was left as a command: %q", a)
		}
		if len(a) > 3 && a[0] == "git" && a[3] == "commit" {
			commits = append(commits, c)
		}
	}
	if len(commits) != 2 || commits[0].Stdin != "fix: first\n\nbody\n" || commits[1].Stdin != "fix: second\n" {
		t.Fatalf("want one commit per call, each with its call's here-document: %+v", commits)
	}
	if got := argvs(t, "commit() { git commit -q -F -; }\nls"); !reflect.DeepEqual(got, [][]string{{"ls"}}) {
		t.Errorf("a function that is never called ran: %q", got)
	}
	if got := argvs(t, "f() { f; }\nf"); len(got) != 1 || got[0][0] != "f" {
		t.Errorf("a function calling itself should stop inlining: %q", got)
	}
}

func TestVariableIsOneWord(t *testing.T) {
	got := argvs(t, "JOB=review-r2\nenvoy run \"$JOB\" --with codex && envoy collect ${JOB}")
	want := [][]string{{"envoy", "run", "review-r2", "--with", "codex"}, {"envoy", "collect", "review-r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// Single quotes keep "$JOB" literal (review r1).
	if got := argvs(t, "JOB=review-r1; envoy run '$JOB'"); !reflect.DeepEqual(got, [][]string{{"envoy", "run", "$JOB"}}) {
		t.Errorf("a single-quoted variable was substituted: %q", got)
	}
	// zsh does not split an unquoted variable, so a command kept in one does
	// not run: "$C -F -" looks for a program named by the whole value.
	for _, c := range split(t, "C=\"git commit -q\" && $C -F - <<'EOF'\nsubject\nEOF") {
		if a := c.Argv(); len(a) > 1 && a[0] == "git" {
			t.Errorf("a variable's value was split into a command: %q", a)
		}
	}
}

// cd moves what follows in its own scope only (review r1).
func TestDirectoryFollowsScope(t *testing.T) {
	dirs := func(src string) []string {
		var out []string
		for _, c := range split(t, src) {
			if a := c.Argv(); len(a) > 0 && a[0] == "git" {
				out = append(out, c.Dir)
			}
		}
		return out
	}
	cases := []struct {
		src  string
		want []string
	}{
		{"cd sub && git status", []string{"/work/app/sub"}},
		{"(cd sub && git status); git status", []string{"/work/app/sub", "/work/app"}},
		{"x=$(cd /tmp && git status); git status", []string{"/tmp", "/work/app"}},
		{"cd sub | cat; git status", []string{"/work/app"}},
		{"cd /abs; cd ..; git status", []string{"/"}},
		{"{ cd sub; }; git status", []string{"/work/app/sub"}},
	}
	for _, tc := range cases {
		if got := dirs(tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.src, got, tc.want)
		}
	}
}

// A subshell and a command substitution keep what they assign and define to
// themselves. A branch decided at run time holds its assignment inside it;
// after it either value may hold, so the variable reads as its source text.
func TestAssignmentsFollowScope(t *testing.T) {
	for src, want := range map[string]string{
		`JOB=old; (JOB=new); envoy run "$JOB"`:                                   "envoy run old",
		`JOB=old; x=$(JOB=new; echo x); envoy run "$JOB"`:                        "envoy run old",
		`JOB=old; JOB=new | cat; envoy run "$JOB"`:                               "envoy run old",
		`(f() { envoy run inner; }); f`:                                          "f",
		`f() { envoy run inner; }; (f)`:                                          "envoy run inner",
		`JOB=old; if test -f x; then JOB=new; fi; envoy run "$JOB"`:              "envoy run $JOB",
		`JOB=old; if test -f x; then JOB=new; envoy run "$JOB"; fi`:              "envoy run new",
		`JOB=old; if test -f x; then JOB=a; else envoy run "$JOB"; fi`:           "envoy run old",
		`JOB=old; test -f x || JOB=new; envoy run "$JOB"`:                        "envoy run $JOB",
		`JOB=old; for v in a b; do N=$JOB; envoy collect "$N"; done`:             "envoy collect old",
		`JOB=old; for v in a b; do JOB=x; done; envoy run "$JOB"`:                "envoy run $JOB",
		`JOB=old; case $1 in a) JOB=new;; esac; envoy run "$JOB"`:                "envoy run $JOB",
		`JOB=old; if test -f x; then OTHER=1; fi; test -f y && envoy run "$JOB"`: "envoy run old",
	} {
		all := argvs(t, src)
		if got := strings.Join(all[len(all)-1], " "); got != want {
			t.Errorf("%s\n  last command: %q, want %q", src, got, want)
		}
	}
}

func TestTextThatDoesNotParse(t *testing.T) {
	for _, src := range []string{`echo "open`, `echo $(ls`, "f() {", "if true; then"} {
		if cmds, err := Split(src, ""); err == nil {
			t.Errorf("%q parsed as %+v", src, cmds)
		}
	}
}
