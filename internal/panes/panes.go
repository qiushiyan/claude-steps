// Package panes asks tmux which panes run a Claude session. It only reads.
package panes

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Pane is a tmux pane whose context chip has published a Claude session id.
type Pane struct {
	ID        string `json:"id"`    // "%12"
	Where     string `json:"where"` // "work:2.1"
	SessionID string `json:"session"`
}

// ErrNoServer means tmux is installed and no server is running.
var ErrNoServer = errors.New("no tmux server")

// The option the dotfiles context chip sets on every live Claude pane.
const format = "#{pane_id}\t#{session_name}:#{window_index}.#{pane_index}\t#{@claude_ctx_sid}"

// List returns the Claude panes of the running tmux server, in tmux's order.
func List() ([]Pane, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, ErrNoServer
		}
		return nil, fmt.Errorf("cannot run tmux: %w", err)
	}
	return Parse(string(out)), nil
}

// Parse reads list-panes output in the package's format, keeping the panes
// that carry a session id.
func Parse(out string) []Pane {
	var panes []Pane
	for line := range strings.Lines(out) {
		f := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
		if len(f) < 3 || f[2] == "" {
			continue
		}
		panes = append(panes, Pane{ID: f[0], Where: f[1], SessionID: f[2]})
	}
	return panes
}
