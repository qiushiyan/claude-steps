package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func home(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	return dir
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsWithNoFile(t *testing.T) {
	dir := home(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectsDir != filepath.Join(dir, ".claude", "projects") || cfg.NotesDir != filepath.Join(dir, ".local", "state", "claude-steps", "notes") {
		t.Errorf("paths: %+v", cfg)
	}
	if len(cfg.Labels) != 0 || len(cfg.Snippets) != 0 {
		t.Errorf("the binary ships labels or snippets: %+v", cfg)
	}
}

func TestLabelsPathsAndSnippets(t *testing.T) {
	dir := home(t)
	state := filepath.Join(dir, "state")
	t.Setenv("XDG_STATE_HOME", state)
	write(t, filepath.Join(dir, ".config", "claude-steps", "config.toml"), `
projects_dir = "~/elsewhere/projects"
snippets = "~/snips.toml"

[[label]]
name = "review"
skills = ["review"]
snippets = ["review-implementation"]
jobs = ["review-"]
count_commits = true

[[label]]
name = "docs"
skills = ["update-docs", "review"]
`)
	write(t, filepath.Join(dir, "snips.toml"), `
trigger = ";;"
providers = ["recent"]

[[snippets]]
key = "review-implementation"
expand = '''
Review the   implementation against the spec,
obligation by obligation, and report every gap you find in it.
'''

[[snippets]]
key = "too-short"
expand = "ok, go ahead"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectsDir != filepath.Join(dir, "elsewhere", "projects") || cfg.NotesDir != filepath.Join(state, "claude-steps", "notes") {
		t.Errorf("paths: %+v", cfg)
	}
	if len(cfg.Labels) != 2 || !cfg.Labels[0].CountCommits || cfg.Labels[1].CountCommits || cfg.Labels[0].Jobs[0] != "review-" {
		t.Errorf("labels: %+v", cfg.Labels)
	}
	if got := strings.Join(cfg.SkillNames(), ","); got != "review,update-docs" {
		t.Errorf("skill names: %s", got)
	}
	if len(cfg.Snippets) != 1 || cfg.Snippets[0].Key != "review-implementation" {
		t.Fatalf("a snippet too short to be distinctive must not be matched: %+v", cfg.Snippets)
	}
	head := cfg.Snippets[0].Head
	if len([]rune(head)) != snippetHead || !strings.HasPrefix(head, "review the implementation against the spec, obligation by") {
		t.Errorf("head: %q", head)
	}
}

// A project's own snippet file is named beside the global one. A file this
// machine lacks holds no snippets, and a key that two files word differently
// is recognised by either wording.
func TestSnippetFilesAreNamed(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, ".config", "claude-steps", "config.toml"),
		`snippets = ["~/global.toml", "~/work/app/.tabtype.local.toml", "~/not/on/this/machine.toml"]`+"\n")
	const (
		reviewVerify = "/review full review. While you wait, prove the intended behaviour again with a spike."
		checkGlobal  = "Review and revise the prompts this session touched against the rulebook, and report."
		checkLocal   = "Review and revise the prompts this session's work touched against the project's guide."
	)
	write(t, filepath.Join(dir, "global.toml"), `
[[snippets]]
key = "review-verify"
expand = "`+reviewVerify+`"

[[snippets]]
key = "prompt-check"
expand = "`+checkGlobal+`"
`)
	write(t, filepath.Join(dir, "work", "app", ".tabtype.local.toml"), `
[[snippets]]
key = "app-review-verify"
expand = """
/review full review. While you wait, run app-verify
on the local rig and compare against a baseline.
"""

[[snippets]]
key = "prompt-check"
expand = "`+checkLocal+`"

[[snippets]]
key = "review-verify"
expand = "`+reviewVerify+`"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range cfg.Snippets {
		keys = append(keys, s.Key)
	}
	if got := strings.Join(keys, " "); got != "review-verify prompt-check app-review-verify prompt-check" {
		t.Errorf("snippets: %s", got)
	}
	for prompt, want := range map[string]string{
		"<pasted_content id=\"1\">\n/review full review. While you wait, run app-verify\non the local rig and compare against a baseline.\n</pasted_content>": "app-review-verify",
		checkGlobal:                     "prompt-check",
		"and then: " + checkLocal:       "prompt-check",
		checkLocal + "\n" + checkGlobal: "prompt-check",
		reviewVerify:                    "review-verify",
		"Review and revise the prompts": "",
	} {
		if got := strings.Join(cfg.Snippets.Pasted(prompt), " "); got != want {
			t.Errorf("%q: pasted %q, want %q", prompt, got, want)
		}
	}
}

// `snippets` is a path or a list of paths, and a file that is there and
// does not decode is an error that names it.
func TestSnippetsWantsPaths(t *testing.T) {
	dir := home(t)
	file := filepath.Join(dir, ".config", "claude-steps", "config.toml")
	for _, bad := range []string{"snippets = 3\n", "snippets = [\"~/a.toml\", 3]\n"} {
		write(t, file, bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "want a path or a list of paths") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
	write(t, file, "snippets = [\"~/a.toml\", \"~/b.toml\"]\n")
	write(t, filepath.Join(dir, "b.toml"), "[[snippets]\nkey = 1\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "b.toml")) {
		t.Errorf("a file that does not decode: got %v", err)
	}
}

func TestXDGConfigHome(t *testing.T) {
	dir := home(t)
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	write(t, filepath.Join(xdg, "claude-steps", "config.toml"), "[[label]]\nname = \"from-xdg\"\n")
	write(t, filepath.Join(dir, ".config", "claude-steps", "config.toml"), "[[label]]\nname = \"from-home\"\n")
	cfg, err := Load()
	if err != nil || len(cfg.Labels) != 1 || cfg.Labels[0].Name != "from-xdg" {
		t.Errorf("got %+v, %v", cfg.Labels, err)
	}
}

// A misspelt key would otherwise leave a column silently empty.
func TestUnknownKeyIsRefused(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, ".config", "claude-steps", "config.toml"), "[[label]]\nname = \"review\"\nskils = [\"review\"]\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "skils") {
		t.Errorf("got %v", err)
	}
	write(t, filepath.Join(dir, ".config", "claude-steps", "config.toml"), "[[label]]\nskills = [\"review\"]\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("got %v", err)
	}
}

// A label may name its hue. Red is not on offer: it marks errors.
func TestLabelColor(t *testing.T) {
	dir := home(t)
	file := filepath.Join(dir, ".config", "claude-steps", "config.toml")
	write(t, file, "[[label]]\nname = \"review\"\ncolor = \"cyan\"\n\n[[label]]\nname = \"docs\"\n")
	cfg, err := Load()
	if err != nil || cfg.Labels[0].Color != "cyan" || cfg.Labels[1].Color != "" {
		t.Errorf("got %+v, %v", cfg.Labels, err)
	}
	write(t, file, "[[label]]\nname = \"review\"\ncolor = \"red\"\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), `color "red" is not one of blue`) {
		t.Errorf("got %v", err)
	}
}
