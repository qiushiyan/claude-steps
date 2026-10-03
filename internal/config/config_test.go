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
