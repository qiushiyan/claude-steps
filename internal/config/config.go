// Package config owns the paths the command reads and writes, and the labels
// that name the board's columns.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Label is a named board column. It groups skills, snippets and envoy jobs
// under one name and carries no state.
type Label struct {
	Name     string   `toml:"name"`
	Skills   []string `toml:"skills"`
	Snippets []string `toml:"snippets"`
	Jobs     []string `toml:"jobs"` // job-name prefixes
	// CountCommits adds the number of commits made since the label's latest
	// event to its cell.
	CountCommits bool `toml:"count_commits"`
	// Color is the hue the label's name is drawn in, one of Colors. Empty
	// takes the next of the default cycle.
	Color string `toml:"color"`
}

// Colors are the hues a label may name. Red is left out: it marks what the
// transcript reports as an error and what the reader could not read.
var Colors = []string{"blue", "magenta", "cyan", "green", "yellow"}

// Snippet is a TabType snippet reduced to what a prompt is matched against.
type Snippet struct {
	Key  string
	Head string // the opening of the snippet's text, squashed
}

type Config struct {
	ProjectsDir string  `toml:"projects_dir"`
	SnippetsSrc string  `toml:"snippets"`
	Labels      []Label `toml:"label"`

	// NotesDir is derived from the environment, never from the file.
	NotesDir string    `toml:"-"`
	Snippets []Snippet `toml:"-"`
}

const (
	snippetHead = 80 // characters of a snippet's opening that must appear in a prompt
	snippetMin  = 40 // a shorter snippet is not distinctive enough to match
)

// Load reads ~/.config/claude-steps/config.toml when it exists and resolves
// every path. A missing file is the default configuration with no labels.
func Load() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("cannot find the home directory: %w", err)
	}
	cfg := Config{
		ProjectsDir: "~/.claude/projects",
		SnippetsSrc: "~/.config/tabtype/config.toml",
	}
	path := filepath.Join(envDir("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "claude-steps", "config.toml")
	meta, err := toml.DecodeFile(path, &cfg)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Config{}, fmt.Errorf("%s: %w", path, err)
	default:
		if extra := meta.Undecoded(); len(extra) > 0 {
			return Config{}, fmt.Errorf("%s: unknown key %q", path, extra[0].String())
		}
	}
	for i, l := range cfg.Labels {
		if l.Name == "" {
			return Config{}, fmt.Errorf("%s: label %d has no name", path, i+1)
		}
		if l.Color != "" && !slices.Contains(Colors, l.Color) {
			return Config{}, fmt.Errorf("%s: label %s: color %q is not one of %s", path, l.Name, l.Color, strings.Join(Colors, ", "))
		}
	}
	cfg.ProjectsDir = expand(cfg.ProjectsDir, home)
	cfg.SnippetsSrc = expand(cfg.SnippetsSrc, home)
	cfg.NotesDir = filepath.Join(envDir("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "claude-steps", "notes")
	cfg.Snippets, err = loadSnippets(cfg.SnippetsSrc)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SkillNames returns every skill name a label lists, the names a prompt can
// mention.
func (c Config) SkillNames() []string {
	var names []string
	seen := map[string]bool{}
	for _, l := range c.Labels {
		for _, s := range l.Skills {
			if !seen[s] {
				seen[s] = true
				names = append(names, s)
			}
		}
	}
	return names
}

func envDir(name, fallback string) string {
	if v := os.Getenv(name); filepath.IsAbs(v) {
		return v
	}
	return fallback
}

func expand(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func loadSnippets(path string) ([]Snippet, error) {
	var file struct {
		Snippets []struct {
			Key    string `toml:"key"`
			Expand string `toml:"expand"`
		} `toml:"snippets"`
	}
	if _, err := toml.DecodeFile(path, &file); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Snippet
	for _, s := range file.Snippets {
		head := []rune(Squash(s.Expand))
		if s.Key == "" || len(head) < snippetMin {
			continue
		}
		if len(head) > snippetHead {
			head = head[:snippetHead]
		}
		out = append(out, Snippet{Key: s.Key, Head: string(head)})
	}
	return out, nil
}

// Squash lowercases text and collapses every run of whitespace to one space,
// the form snippets and prompts are compared in.
func Squash(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}
