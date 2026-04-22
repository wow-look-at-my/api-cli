package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the top-level JSON schema: the CLI's name, help, defaults, and
// command tree.
type Config struct {
	Name     string    `json:"name"`
	Short    string    `json:"short,omitempty"`
	Long     string    `json:"long,omitempty"`
	Defaults Defaults  `json:"defaults"`
	Commands []Command `json:"commands,omitempty"`
}

// Defaults is applied to every request — the base URL is prefixed onto each
// leaf's path, and the headers merge with per-request headers (request wins
// on collision).
type Defaults struct {
	BaseURL string            `json:"base_url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Command is a node in the CLI tree. A node is a leaf (issues an HTTP call)
// iff Request is non-nil; otherwise it's a group that prints help.
type Command struct {
	Name     string    `json:"name"`
	Short    string    `json:"short,omitempty"`
	Long     string    `json:"long,omitempty"`
	Args     []Arg     `json:"args,omitempty"`
	Flags    []Flag    `json:"flags,omitempty"`
	Request  *Request  `json:"request,omitempty"`
	Commands []Command `json:"commands,omitempty"`
}

// Arg is a positional argument.
type Arg struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // "string"|"int"; default "string"
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// Flag is a named flag. Type is one of: "string", "bool", "int", "string-slice".
type Flag struct {
	Name        string `json:"name"`
	Short       string `json:"short,omitempty"`
	Type        string `json:"type,omitempty"` // default "string"
	Default     any    `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// Request describes the outbound HTTP call. All string fields support
// text/template placeholders ({{.argName}}, {{.flagName}}, {{.env.VAR}}).
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
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

// Load reads and parses a config file. Unknown keys are rejected.
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
	if strings.TrimSpace(cfg.Defaults.BaseURL) == "" {
		return fmt.Errorf("defaults.base_url is required")
	}
	seen := map[string]bool{}
	for i, c := range cfg.Commands {
		where := fmt.Sprintf("commands[%d]", i)
		if err := validateCommand(&c, where, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateCommand(c *Command, where string, siblings map[string]bool) error {
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

	// Validate args.
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

	// Validate flags.
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
		if argNames[fl.Name] {
			return fmt.Errorf("%s: flag %q collides with an arg of the same name (they share the template namespace)", fw, fl.Name)
		}
	}

	// A node must be either a group (has subcommands, no request) or a leaf
	// (has request). A leaf may still have subcommands, but it must have
	// method+path.
	if c.Request != nil {
		if strings.TrimSpace(c.Request.Method) == "" {
			return fmt.Errorf("%s.request.method is required", where)
		}
		if strings.TrimSpace(c.Request.Path) == "" {
			return fmt.Errorf("%s.request.path is required", where)
		}
	} else if len(c.Commands) == 0 {
		return fmt.Errorf("%s: a node with no subcommands must have a request", where)
	}

	// Recurse.
	childSeen := map[string]bool{}
	for i, child := range c.Commands {
		cw := fmt.Sprintf("%s.commands[%d]", where, i)
		if err := validateCommand(&child, cw, childSeen); err != nil {
			return err
		}
	}
	return nil
}
