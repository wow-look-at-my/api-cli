package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the top-level JSON schema.
//
// The CLI is a declarative alias system: each leaf command renders a `command`
// template (inherited from an ancestor or overridden locally) against a data
// context composed of args, flags, environment, vars, and the leaf's entry
// variables. The rendered command is then executed.
type Config struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Vars        map[string]any `json:"vars,omitempty"`
	Command     *Cmd           `json:"command,omitempty"`
	Commands    []Command      `json:"commands,omitempty"`
}

// Command is a node in the CLI tree. A node is a leaf iff it has no
// subcommands; leaves must have a command available (own or inherited). Group
// nodes just print help.
//
// `entry` is arbitrary user-defined JSON — its string leaves are
// template-rendered against {arg, flag, env, var} and the result is exposed
// to the command template as `.entry`.
//
// `vars` merges into the ancestor chain, with the child winning on key
// collision. `command`, if set, overrides the inherited command for this
// subtree.
type Command struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Args        []Arg          `json:"args,omitempty"`
	Flags       []Flag         `json:"flags,omitempty"`
	Vars        map[string]any `json:"vars,omitempty"`
	Command     *Cmd           `json:"command,omitempty"`
	Entry       json.RawMessage `json:"entry,omitempty"`
	Commands    []Command      `json:"commands,omitempty"`
}

// Arg is a positional argument. Type is "string" or "int" (default string).
type Arg struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// Flag is a named flag. Type is string|bool|int|string-slice (default string).
type Flag struct {
	Name        string `json:"name"`
	Short       string `json:"short,omitempty"`
	Type        string `json:"type,omitempty"`
	Default     any    `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// Cmd is the executable form of a command. In JSON it may be either:
//
//   - A string: rendered as a template, then executed via "sh -c <rendered>".
//     Best for pipelines and anything that benefits from shell features.
//     The author is responsible for quoting interpolated values (use the
//     `shellquote` helper).
//
//   - An array of strings: each element is rendered as a template, and the
//     result is executed directly via exec (no shell). Safer — no quoting
//     concerns — but no shell features.
type Cmd struct {
	Shell    bool     // true if the source was a scalar string
	Template string   // source for the scalar (string) form
	Argv     []string // source for the argv (array) form
}

// UnmarshalJSON accepts either a string or an array of strings.
func (c *Cmd) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		c.Shell = true
		c.Template = s
		return nil
	}
	if trimmed[0] == '[' {
		var a []string
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		c.Argv = a
		return nil
	}
	return fmt.Errorf("command must be a string or array of strings; got %s", trimmed)
}

// MarshalJSON emits whichever form was loaded. Mostly for tests/debugging.
func (c *Cmd) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	if c.Shell {
		return json.Marshal(c.Template)
	}
	return json.Marshal(c.Argv)
}

// Defined reports whether the command has any template to execute.
func (c *Cmd) Defined() bool {
	if c == nil {
		return false
	}
	return c.Shell || len(c.Argv) > 0
}

var reservedCommandNames = map[string]bool{
	"help":       true,
	"completion": true,
	"__complete": true,
}

var validFlagTypes = map[string]bool{
	"":             true, // empty defaults to "string"
	"string":       true,
	"bool":         true,
	"int":          true,
	"string-slice": true,
}

var validArgTypes = map[string]bool{
	"":       true, // defaults to "string"
	"string": true,
	"int":    true,
}

// Load reads and parses a config file. Unknown keys are rejected to catch
// typos early.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("top-level \"name\" is required")
	}
	seen := map[string]bool{}
	hasRootCmd := cfg.Command.Defined()
	for i, c := range cfg.Commands {
		where := fmt.Sprintf("commands[%d]", i)
		if err := validateCommand(&c, where, seen, hasRootCmd); err != nil {
			return err
		}
	}
	return nil
}

// validateCommand enforces schema invariants. inheritedCmd indicates whether
// an ancestor has a command template available (we need at least one to reach
// a leaf).
func validateCommand(c *Command, where string, siblings map[string]bool, inheritedCmd bool) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%s: \"name\" is required", where)
	}
	if strings.ContainsAny(c.Name, " \t\n/") {
		return fmt.Errorf("%s: name %q must not contain whitespace or slashes", where, c.Name)
	}
	if reservedCommandNames[c.Name] {
		return fmt.Errorf("%s: name %q is reserved by cobra", where, c.Name)
	}
	if siblings[c.Name] {
		return fmt.Errorf("%s: duplicate command name %q", where, c.Name)
	}
	siblings[c.Name] = true

	argNames := map[string]bool{}
	requiredAfterOptional := false
	for i, a := range c.Args {
		aw := fmt.Sprintf("%s.args[%d]", where, i)
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%s: name required", aw)
		}
		if !validArgTypes[a.Type] {
			return fmt.Errorf("%s: type %q must be one of string|int", aw, a.Type)
		}
		if argNames[a.Name] {
			return fmt.Errorf("%s: duplicate arg name %q", aw, a.Name)
		}
		argNames[a.Name] = true
		if !a.Required {
			requiredAfterOptional = true
		} else if requiredAfterOptional {
			return fmt.Errorf("%s: required arg %q cannot follow an optional arg", aw, a.Name)
		}
	}

	flagNames := map[string]bool{}
	flagShorts := map[string]bool{}
	for i, fl := range c.Flags {
		fw := fmt.Sprintf("%s.flags[%d]", where, i)
		if strings.TrimSpace(fl.Name) == "" {
			return fmt.Errorf("%s: name required", fw)
		}
		if !validFlagTypes[fl.Type] {
			return fmt.Errorf("%s: type %q must be one of string|bool|int|string-slice", fw, fl.Type)
		}
		if flagNames[fl.Name] {
			return fmt.Errorf("%s: duplicate flag name %q", fw, fl.Name)
		}
		flagNames[fl.Name] = true
		if fl.Short != "" {
			if len(fl.Short) != 1 {
				return fmt.Errorf("%s: short %q must be a single character", fw, fl.Short)
			}
			if flagShorts[fl.Short] {
				return fmt.Errorf("%s: duplicate short %q", fw, fl.Short)
			}
			flagShorts[fl.Short] = true
		}
	}

	haveCmd := inheritedCmd || c.Command.Defined()

	// Leaf: must have a command available.
	if len(c.Commands) == 0 {
		if !haveCmd {
			return fmt.Errorf("%s: leaf has no command and no ancestor defines one", where)
		}
	}

	// `entry` only makes sense on a leaf (it's the leaf's variable bag).
	if len(c.Entry) > 0 && len(c.Commands) > 0 {
		return fmt.Errorf("%s: `entry` is only allowed on leaves (nodes with no subcommands)", where)
	}

	childSeen := map[string]bool{}
	for i, child := range c.Commands {
		cw := fmt.Sprintf("%s.commands[%d]", where, i)
		if err := validateCommand(&child, cw, childSeen, haveCmd); err != nil {
			return err
		}
	}
	return nil
}
