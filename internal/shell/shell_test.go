package shell

import (
	"reflect"
	"testing"
)

func argvs(src string) [][]string {
	var out [][]string
	for _, c := range Split(src) {
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
		{"subshell and keywords", `if true; then (cd x && git commit -m s); fi`, [][]string{{"true"}, {"cd", "x"}, {"git", "commit", "-m", "s"}, {"fi"}}},
		{"substitution holds commands", `S=$(git rev-parse HEAD) && echo "$S"`, [][]string{{"git", "rev-parse", "HEAD"}, nil, {"echo", "$(git rev-parse HEAD)"}}},
		{"background", `envoy run consult-r1 --with codex & wait`, [][]string{{"envoy", "run", "consult-r1", "--with", "codex"}, {"wait"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvs(tc.src); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Split(%q)\n got %q\nwant %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestHeredocMessageIsOneWord(t *testing.T) {
	src := "git add f && git commit -q -m \"$(cat <<'EOF'\nfix: the \"quoted\" thing; git commit && more\n\nbody line\nEOF\n)\" && git log -1"
	cmds := Split(src)
	var commit []string
	for _, c := range cmds {
		if a := c.Argv(); len(a) > 1 && a[0] == "git" && a[1] == "commit" {
			commit = a
		}
	}
	want := []string{"git", "commit", "-q", "-m", "fix: the \"quoted\" thing; git commit && more\n\nbody line\n"}
	if !reflect.DeepEqual(commit, want) {
		t.Fatalf("commit argv\n got %q\nwant %q", commit, want)
	}
	last := cmds[len(cmds)-1].Argv()
	if !reflect.DeepEqual(last, []string{"git", "log", "-1"}) {
		t.Errorf("command after the here-document: got %q", last)
	}
	for _, c := range cmds {
		if a := c.Argv(); len(a) > 0 && a[0] == "more" {
			t.Errorf("here-document text leaked into a command: %q", a)
		}
	}
}

func TestHeredocOnStandardInput(t *testing.T) {
	cmds := Split("git commit -F - <<'MSG'\nsubject here\n\nbody\nMSG\necho done")
	if len(cmds) != 2 {
		t.Fatalf("got %d commands: %+v", len(cmds), cmds)
	}
	if !cmds[0].HasHeredoc || cmds[0].Heredoc != "subject here\n\nbody\n" {
		t.Errorf("here-document body: %q", cmds[0].Heredoc)
	}
	if got := cmds[0].Argv(); !reflect.DeepEqual(got, []string{"git", "commit", "-F", "-"}) {
		t.Errorf("argv: %q", got)
	}
}

func TestFunctionRunsWhereItIsCalled(t *testing.T) {
	src := "git reset -q && commit() { git -c core.hooksPath=.githooks commit -q -F -; } && \\\ngit add a.ts && commit <<'EOF'\nfix: first\n\nbody\nEOF\ngit add b.ts && commit <<'EOF'\nfix: second\nEOF"
	var commits []Command
	for _, c := range Split(src) {
		a := c.Argv()
		if len(a) > 0 && a[0] == "commit" {
			t.Errorf("the function's name was left as a command: %q", a)
		}
		if len(a) > 3 && a[0] == "git" && a[3] == "commit" {
			commits = append(commits, c)
		}
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commit commands, want one per call: %+v", len(commits), commits)
	}
	if commits[0].Heredoc != "fix: first\n\nbody\n" || commits[1].Heredoc != "fix: second\n" {
		t.Errorf("each call's here-document should reach the body: %q, %q", commits[0].Heredoc, commits[1].Heredoc)
	}
}

func TestDefinedFunctionThatIsNeverCalledRunsNothing(t *testing.T) {
	if got := argvs("commit() { git commit -q -F -; }\nls"); !reflect.DeepEqual(got, [][]string{{"ls"}}) {
		t.Errorf("got %q", got)
	}
}

func TestVariableIsOneWord(t *testing.T) {
	got := argvs("JOB=review-r2\nenvoy run \"$JOB\" --with codex && envoy collect ${JOB}")
	want := [][]string{nil, {"envoy", "run", "review-r2", "--with", "codex"}, {"envoy", "collect", "review-r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// zsh does not split an unquoted variable, so a command kept in one does
	// not run: "$C -F -" looks for a program named by the whole value.
	for _, c := range Split("C=\"git commit -q\" && $C -F - <<'EOF'\nsubject\nEOF") {
		if a := c.Argv(); len(a) > 1 && a[0] == "git" {
			t.Errorf("a variable's value was split into a command: %q", a)
		}
	}
}

func TestUnterminatedInputDoesNotPanic(t *testing.T) {
	for _, src := range []string{`echo "open`, `echo 'open`, "cat <<EOF\nnever closed", `echo $(ls`, "x <<", `a \`, "`open", "<<<", "f() {", "f(", "f() { ls"} {
		Split(src)
	}
}
