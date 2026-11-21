// The browser's saved state: what the user typed, and where they were, kept between sessions.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// browseState is the file the browser saves. Results are not saved, because a result goes stale.
type browseState struct {
	// Memory is the last value typed for each input name.
	Memory map[string]string `json:"memory,omitempty"`
	// Forms holds each command's form, by command path, then by field id.
	Forms map[string]map[string]string `json:"forms,omitempty"`
	// Current is the path of the command the user had picked.
	Current string `json:"current,omitempty"`
}

// secretInput matches an input name whose value must never reach the disk.
var secretInput = regexp.MustCompile(`(?i)token|secret|passw|credential|auth|api[-_]?key|private`)

// statePath is the file that holds a config's saved state. Configs with the same name in different directories
// keep separate files.
func statePath(cfg *Config) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(cfg.Dir + "\x00" + cfg.Name))
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '.' || r < ' ' {
			return '_'
		}
		return r
	}, cfg.Name)
	return filepath.Join(dir, "api-cli", name+"-"+hex.EncodeToString(sum[:4])+".json"), nil
}

// loadState reads a saved state. A missing file is an empty state. A file that does not parse is an error, so the
// caller can say so rather than quietly drop what the user saved.
func loadState(path string) (browseState, error) {
	var st browseState
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return browseState{}, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// saveState writes the state through a temporary file, so a crash mid-write
// leaves the file whole.
func saveState(path string, st browseState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// attachState gives the model a state file: it restores what the file holds, and saves to it from then on.
// A file that does not read is reported on the status line, and the session starts empty.
func (m *browseModel) attachState(path string) {
	m.statePath = path
	st, err := loadState(path)
	if err != nil {
		m.setStatus("saved state not restored: "+err.Error(), "bad")
		return
	}
	for k, v := range st.Memory {
		if !secretInput.MatchString(k) {
			m.memory[k] = v
		}
	}
	for path, values := range st.Forms {
		if e := m.byPath[path]; e != nil {
			e.restore(values)
		}
	}
	if e := m.byPath[st.Current]; e != nil {
		m.reveal(e)
		m.cur = e
	}
}

// state is what the model saves: the memory, every form that holds a value, and the picked command. A secret input
// is left out of all of it.
func (m *browseModel) state() browseState {
	st := browseState{Memory: map[string]string{}, Forms: map[string]map[string]string{}, Current: m.cur.pathString()}
	for k, v := range m.memory {
		if !secretInput.MatchString(k) {
			st.Memory[k] = v
		}
	}
	for _, e := range m.entries {
		values := map[string]string{}
		for i := range e.fields {
			f := &e.fields[i]
			if f.isBool() || f.value == "" || secretInput.MatchString(f.name()) {
				continue
			}
			values[f.id] = f.value
		}
		if len(values) > 0 {
			st.Forms[e.pathString()] = values
		}
	}
	return st
}

// save writes the state file, when the model has one. A failed write says so on the status line.
func (m *browseModel) save() {
	if m.statePath == "" {
		return
	}
	if err := saveState(m.statePath, m.state()); err != nil {
		m.setStatus("state not saved: "+err.Error(), "bad")
	}
}
