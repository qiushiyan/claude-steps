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
	Key string
	// Head is the opening of the snippet's text in the form prompts are
	// compared in: lower case, every run of whitespace one space.
	Head string
}

// Snippets are the snippets a prompt can paste, from every file the
// configuration names. A key that two files give two wordings stands twice.
type Snippets []Snippet

// Pasted returns the keys of the snippets whose opening the prompt holds,
// each once.
func (ss Snippets) Pasted(prompt string) []string {
	var keys []string
	flat := squash(prompt)
	for _, s := range ss {
		if strings.Contains(flat, s.Head) && !slices.Contains(keys, s.Key) {
			keys = append(keys, s.Key)
		}
	}
	return keys
}

// paths is one path or a list of them. `snippets` named one file before a
// project's own could stand beside the global one, and a string still does.
type paths []string

func (p *paths) UnmarshalTOML(v any) error {
	wrong := errors.New("snippets: want a path or a list of paths")
	switch v := v.(type) {
	case string:
		*p = paths{v}
	case []any:
		out := paths{}
		for _, e := range v {
			path, ok := e.(string)
			if !ok {
				return wrong
			}
			out = append(out, path)
		}
		*p = out
	default:
		return wrong
	}
	return nil
}

type Config struct {
	ProjectsDir string `toml:"projects_dir"`
	// SnippetFiles are the TabType files a prompt is matched against. They
	// are named, never found from a session's directory: a session outlives
	// its worktree, and would read differently once that is removed.
	SnippetFiles paths   `toml:"snippets"`
	Labels       []Label `toml:"label"`

	// NotesDir is derived from the environment, never from the file.
	NotesDir string   `toml:"-"`
	Snippets Snippets `toml:"-"`
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
		ProjectsDir:  "~/.claude/projects",
		SnippetFiles: paths{"~/.config/tabtype/config.toml"},
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
	for i, file := range cfg.SnippetFiles {
		cfg.SnippetFiles[i] = expand(file, home)
	}
	cfg.NotesDir = filepath.Join(envDir("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "claude-steps", "notes")
	cfg.Snippets, err = loadSnippets(cfg.SnippetFiles)
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

// loadSnippets reads the snippets of every file, in the order named. A file
// that does not exist holds none: one configuration serves machines that lack
// a project's checkout. A file that cannot be decoded is an error.
func loadSnippets(files []string) (Snippets, error) {
	var out Snippets
	for _, path := range files {
		var file struct {
			Snippets []struct {
				Key    string `toml:"key"`
				Expand string `toml:"expand"`
			} `toml:"snippets"`
		}
		if _, err := toml.DecodeFile(path, &file); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, s := range file.Snippets {
			head := []rune(squash(s.Expand))
			if s.Key == "" || len(head) < snippetMin {
				continue
			}
			if len(head) > snippetHead {
				head = head[:snippetHead]
			}
			if snippet := (Snippet{Key: s.Key, Head: string(head)}); !slices.Contains(out, snippet) {
				out = append(out, snippet)
			}
		}
	}
	return out, nil
}

// squash lowercases text and collapses every run of whitespace to one space,
// the form snippets and prompts are compared in.
func squash(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}
