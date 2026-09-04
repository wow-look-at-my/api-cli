package main

import (
	"encoding/json"
	"fmt"
	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/go-containers/set"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the top-level JSON schema.
//
// The CLI is a declarative alias system: each leaf command renders a `command`
// template (inherited from an ancestor or overridden locally) against a data
// context composed of args, flags, environment, vars, and the leaf's entry
// variables. The rendered command is then executed.
type Config struct {
	Schema      string                `json:"$schema,omitempty"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Vars        map[string]any        `json:"vars,omitempty"`
	Command     *Cmd                  `json:"command,omitempty"`
	Request     *Request              `json:"request,omitempty"`
	Cwd         string                `json:"cwd,omitempty"`
	Stdin       string                `json:"stdin,omitempty"`
	Formats     map[string]*Format    `json:"formats,omitempty"`
	Transports  map[string]*Transport `json:"transports,omitempty"`
	Downloads   *Downloads            `json:"downloads,omitempty"`
	Commands    []Command             `json:"commands,omitempty"`
	// Dir is the directory the config was read from. A <tml src=> resolves
	// against it, so a path in the config means what the author sees next to
	// the file rather than wherever the shell happens to sit.
	Dir string `json:"-"`
}

// Command is a node in the CLI tree. A node is a leaf iff it has no
// subcommands; leaves must have a command available (own or inherited). Group
// nodes just print help.
//
// `entry` is arbitrary user-defined JSON — its string leaves are
// template-rendered against {arg, flag, env, var, result} and the result is
// exposed to the command template as `.entry`.
//
// `vars` merges into the ancestor chain, with the child winning on key
// collision. `command`, if set, overrides the inherited command for this
// subtree.
//
// `cwd`, if set, overrides the inherited working directory for this subtree.
// Like `command`, it inherits along the tree (closest non-empty ancestor wins)
// and is rendered as a Go template against the same data context as the
// command it applies to.
//
// `stdin`, if set, overrides the inherited stdin template for this subtree.
// The rendered string is fed to the child process's stdin. When empty/unset,
// the child inherits the parent process's stdin.
//
// `steps` run sequentially before the leaf's own run. A step runs a command or
// a request — the same fork as `<run>` — and defaults to the leaf's effective
// run when it declares neither. Each step's output is captured and parsed as
// JSON (or kept as a raw string if not valid JSON), then stored in
// `.result.<name>` for use by subsequent steps and the final run.
type Command struct {
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	Passthrough   bool            `json:"passthrough,omitempty"`
	Args          []Arg           `json:"args,omitempty"`
	Flags         []Flag          `json:"flags,omitempty"`
	Vars          map[string]any  `json:"vars,omitempty"`
	Command       *Cmd            `json:"command,omitempty"`
	Request       *Request        `json:"request,omitempty"`
	Cwd           string          `json:"cwd,omitempty"`
	Stdin         string          `json:"stdin,omitempty"`
	Steps         []Step          `json:"steps,omitempty"`
	Entry         json.RawMessage `json:"entry,omitempty"`
	Preconditions []string        `json:"preconditions,omitempty"`
	Confirm       string          `json:"confirm,omitempty"`
	Format        *FormatRef      `json:"format,omitempty"`
	Fields        *Fields         `json:"fields,omitempty"`
	TML           *TML            `json:"tml,omitempty"`
	Downloads     []Download      `json:"downloads,omitempty"`
	Commands      []Command       `json:"commands,omitempty"`
}

// Format is a presentation-layer renderer applied to a leaf command's stdout
// before it reaches the user. Inspired by PowerShell's .format.ps1xml.
//
// `Input` selects how the captured stdout is parsed for the template:
// "json" (default; uses parseResult), "lines" (split on \n), or "raw" (as-is).
//
// `When` is a Go-template predicate evaluated against the format context
// (.tty, .width, .data, .arg, .flag, .env, .var, .entry, .result). Empty
// defaults to "{{.tty}}" — format only on a TTY. Output is rendered iff the
// predicate is truthy AND the user has not opted out (--no-format,
// --format=raw, NO_FORMAT=1).
//
// `Views` are alternative renderings; selectView decides which one applies.
type Format struct {
	Input string `json:"input,omitempty"`
	When  string `json:"when,omitempty"`
	Views []View `json:"views"`
}

// View is one alternative rendering inside a Format. Selection rules:
//  1. --view=<name> from the user wins if set.
//  2. Else first view whose `When` predicate renders truthy wins.
//  3. Else first view with `Default: true`.
//  4. Else first view in the slice.
type View struct {
	Name     string `json:"name"`
	When     string `json:"when,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Template string `json:"template"`
}

// FormatRef is the type stored on Command.Format. JSON shape is either:
//
//   - A string: a name into Config.Formats (the named-reference form).
//   - An object with `views`: an inline Format definition.
//
// Mirrors how *Cmd accepts string-or-array.
type FormatRef struct {
	Name   string  // set when JSON was a string
	Inline *Format // set when JSON was an inline object
}

// UnmarshalJSON accepts either a string or an inline format object.
func (r *FormatRef) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		r.Name = s
		return nil
	}
	if trimmed[0] == '{' {
		var f Format
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			return err
		}
		r.Inline = &f
		return nil
	}
	return fmt.Errorf("format must be a string or object; got %s", trimmed)
}

// MarshalJSON emits whichever form was loaded. Mostly for tests.
func (r *FormatRef) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	if r.Inline != nil {
		return json.Marshal(r.Inline)
	}
	return json.Marshal(r.Name)
}

// Defined reports whether the ref points at any format.
func (r *FormatRef) Defined() bool {
	if r == nil {
		return false
	}
	return r.Name != "" || r.Inline != nil
}

// Step is a pre-execution stage on a leaf command. Its output is captured,
// parsed as JSON if valid, and stored under `.result.<name>` for use in
// subsequent steps and the leaf's own entry/command templates.
type Step struct {
	Name string `json:"name"`
	When string `json:"when,omitempty"`
	// Over repeats the step once per element of a list an earlier result
	// holds. The step sees the element as `.item` and its position as
	// `.index`, and the result is a list of {"item": element, "result":
	// response} in the source order. So one screen can show a row per build
	// AND what a second call says about each of them.
	Over    string          `json:"over,omitempty"`
	Entry   json.RawMessage `json:"entry,omitempty"`
	Command *Cmd            `json:"command,omitempty"`
	Request *Request        `json:"request,omitempty"`
	Cwd     string          `json:"cwd,omitempty"`
	Stdin   string          `json:"stdin,omitempty"`
}

// Arg is a positional argument. Type is "string" or "int" (default string).
//
// If Variadic is true, the arg consumes all remaining positional values into a
// []string (or []int) and must be the last entry in the args list. A required
// variadic arg requires at least one value; an optional variadic arg accepts
// zero or more.
type Arg struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Variadic    bool   `json:"variadic,omitempty"`
	Description string `json:"description,omitempty"`
}

// Flag is a named flag. Type is string|bool|int|string-slice (default string).
//
// `Default` for a string flag may itself be a template — it is rendered at
// invocation time against {arg, flag, env, var}, so a flag default can be
// derived from another arg or flag. Templated defaults only apply to the
// "string" type.
//
// `Conflicts` lists sibling flag names that may not be set together; the CLI
// rejects the invocation if more than one is supplied.
type Flag struct {
	Name        string   `json:"name"`
	Short       string   `json:"short,omitempty"`
	Type        string   `json:"type,omitempty"`
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Conflicts   []string `json:"conflicts,omitempty"`
	Description string   `json:"description,omitempty"`
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

// Request is a first-class HTTP request, the alternative to a shell/argv Cmd as
// a leaf's executable. It is built from a <run><request> element. Like Cmd it
// inherits down the command tree: the closest ancestor that defines a <run>
// (whether request or command) wins for a subtree.
//
// Every string field is a Go template rendered against the leaf's data context
// (.arg, .flag, .env, .var, .entry, .result) at invocation time.
type Request struct {
	Method    string    `json:"method,omitempty"`    // template; defaults to GET
	URL       string    `json:"url"`                 // template
	QueryFrom string    `json:"queryFrom,omitempty"` // context path to a map of params (<query from=>)
	Query     []Param   `json:"query,omitempty"`     // explicit <param> children
	Headers   []Header  `json:"headers,omitempty"`
	Body      string    `json:"body,omitempty"`      // template; empty means no body
	Response  *Response `json:"response,omitempty"`  // nil means stream the raw body
	Transport string    `json:"transport,omitempty"` // registry name; empty means the config default
}

// Transport is a user-supplied program that performs requests in place of the
// built-in net/http client — for APIs whose authentication or wire format is
// easier to delegate to an existing tool than to reproduce here.
//
// The program receives the fully-rendered request at `.request` (method, url,
// body, headers, header_lines) on top of the leaf's own data context, so its
// argv is written with the same placeholders as any other command. Its stdout
// is the response body and feeds `<response jq=>` exactly like a built-in
// response; a non-zero exit fails the request.
//
// Stdin is the request body unless the transport declares its own `<stdin>`.
// Either way it is explicit: a transport never inherits the user's terminal,
// so a program that reads stdin cannot hang waiting for one.
type Transport struct {
	Name     string `json:"name"`
	Command  *Cmd   `json:"command,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Stdin    string `json:"stdin,omitempty"`
	StdinSet bool   `json:"stdinSet,omitempty"` // distinguishes <stdin/> (send nothing) from no <stdin> (send the body)
	Default  bool   `json:"default,omitempty"`
}

// Defined reports whether the request has anything to execute (a URL).
func (r *Request) Defined() bool {
	return r != nil && strings.TrimSpace(r.URL) != ""
}

// Param is one query parameter. Name and Value are templates. When, if
// non-empty, is a context path: the param is included only when it is truthy
// (set by an enclosing <if test=>).
type Param struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	When  string `json:"when,omitempty"`
}

// Header is one request header. Name and Value are templates. When mirrors
// Param.When for headers wrapped in <if test=>.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	When  string `json:"when,omitempty"`
}

// Response shapes a JSON response body before output. JQ is the jq program, as
// a template, or a bare dotted context path naming one (e.g. "var.filter") —
// see jqProgram. The program runs over the decoded body and the result(s) are
// emitted as JSON.
type Response struct {
	JQ string `json:"jq,omitempty"`
}

// Fields and Field are the fields package's own declarations. They are aliased
// rather than redeclared so a <fields> element parses straight into the types
// the renderer takes, and an importing program shares them.
type (
	Fields = fields.Fields
	Field  = fields.Field
)

// reservedCommandNames are the names cobra owns. A config cannot declare one.
var reservedCommandNames = set.Of("help", "completion", "__complete", "docs")

// validFlagTypes are the accepted <flag type=> values. The empty string
// defaults to "string".
var validFlagTypes = set.Of("", "string", "bool", "int", "string-slice")

// validArgTypes are the accepted <arg type=> values. The empty string defaults
// to "string".
var validArgTypes = set.Of("", "string", "int")

// validFormatInputs are the accepted <format input=> values. The empty string
// defaults to "json".
var validFormatInputs = set.Of("", "json", "lines", "raw")

// Load reads and parses an XML config file. The XML element tree is mapped to
// the Config model by parseConfigXML (see xmlsource.go); node placeholders
// (<value>/<if>/<for>) are compiled to Go templates there. Unknown elements
// and attributes are rejected to catch typos early.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}

	cfg, err := parseConfigXML(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	cfg.Dir = filepath.Dir(path)
	return cfg, nil
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("top-level \"name\" is required")
	}
	for name, f := range cfg.Formats {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("formats: empty name")
		}
		if err := validateFormat(f, fmt.Sprintf("formats[%q]", name)); err != nil {
			return err
		}
	}
	if err := validateTransports(cfg.Transports); err != nil {
		return err
	}
	if err := validateDownloadSettings(cfg.Downloads); err != nil {
		return err
	}
	if cfg.Command.Defined() && cfg.Request.Defined() {
		return fmt.Errorf("top-level: a <run> is either a command or a request, not both")
	}
	if err := validateRequest(cfg.Request, "request", cfg.Transports); err != nil {
		return err
	}
	seen := map[string]bool{}
	hasRootRun := cfg.Command.Defined() || cfg.Request.Defined()
	for i, c := range cfg.Commands {
		where := fmt.Sprintf("commands[%d]", i)
		if err := validateCommand(&c, where, seen, hasRootRun, cfg.Formats, cfg.Transports); err != nil {
			return err
		}
	}
	return nil
}

// validateTransports checks the transport registry: every entry needs a
// command to run, and at most one may claim the default.
func validateTransports(transports map[string]*Transport) error {
	names := make([]string, 0, len(transports))
	for name := range transports {
		names = append(names, name)
	}
	sort.Strings(names)

	defaulted := ""
	for _, name := range names {
		where := fmt.Sprintf("transports[%q]", name)
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("transports: empty name")
		}
		if name == builtinTransportName {
			return fmt.Errorf("%s: %q is reserved for the built-in HTTP client", where, builtinTransportName)
		}
		t := transports[name]
		if t == nil || !t.Command.Defined() {
			return fmt.Errorf("%s: <transport> requires a <run> command", where)
		}
		if !t.Default {
			continue
		}
		if defaulted != "" {
			return fmt.Errorf("%s: transport %q is already the default; only one may be", where, defaulted)
		}
		defaulted = name
	}
	return nil
}

// validateRequest checks a request's invariants. A nil request is fine
// (no request configured at this node).
func validateRequest(r *Request, where string, transports map[string]*Transport) error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(r.URL) == "" {
		return fmt.Errorf("%s: <request> requires a <url>", where)
	}
	name := strings.TrimSpace(r.Transport)
	if name == "" || name == builtinTransportName {
		return nil
	}
	if _, ok := transports[name]; !ok {
		return fmt.Errorf("%s: references unknown transport %q", where, name)
	}
	return nil
}

func validateFormat(f *Format, where string) error {
	if f == nil {
		return fmt.Errorf("%s: empty format", where)
	}
	if !validFormatInputs.Contains(f.Input) {
		return fmt.Errorf("%s: input %q must be one of json|lines|raw", where, f.Input)
	}
	if len(f.Views) == 0 {
		return fmt.Errorf("%s: at least one view is required", where)
	}
	viewNames := set.New[string]()
	for i, v := range f.Views {
		vw := fmt.Sprintf("%s.views[%d]", where, i)
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("%s: name required", vw)
		}
		if viewNames.Contains(v.Name) {
			return fmt.Errorf("%s: duplicate view name %q", vw, v.Name)
		}
		viewNames.Add(v.Name)
		if strings.TrimSpace(v.Template) == "" {
			return fmt.Errorf("%s: template required", vw)
		}
	}
	return nil
}

// validateCommand enforces schema invariants. inheritedCmd indicates whether
// an ancestor has a command template available (we need at least one to reach
// a leaf). formats is the top-level format registry; named refs resolve into
// it.
func validateCommand(c *Command, where string, siblings map[string]bool, inheritedRun bool, formats map[string]*Format, transports map[string]*Transport) error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%s: \"name\" is required", where)
	}
	if strings.ContainsAny(c.Name, " \t\n/") {
		return fmt.Errorf("%s: name %q must not contain whitespace or slashes", where, c.Name)
	}
	if reservedCommandNames.Contains(c.Name) {
		return fmt.Errorf("%s: name %q is reserved by cobra", where, c.Name)
	}
	if siblings[c.Name] {
		return fmt.Errorf("%s: duplicate command name %q", where, c.Name)
	}
	siblings[c.Name] = true

	if c.Passthrough && len(c.Args) > 0 {
		return fmt.Errorf("%s: passthrough commands cannot declare args (use .rest instead)", where)
	}
	if c.Passthrough && len(c.Commands) > 0 {
		return fmt.Errorf("%s: passthrough is only allowed on leaves", where)
	}

	argNames := set.New[string]()
	requiredAfterOptional := false
	for i, a := range c.Args {
		aw := fmt.Sprintf("%s.args[%d]", where, i)
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%s: name required", aw)
		}
		if !validArgTypes.Contains(a.Type) {
			return fmt.Errorf("%s: type %q must be one of string|int", aw, a.Type)
		}
		if argNames.Contains(a.Name) {
			return fmt.Errorf("%s: duplicate arg name %q", aw, a.Name)
		}
		argNames.Add(a.Name)
		if a.Variadic && i != len(c.Args)-1 {
			return fmt.Errorf("%s: variadic arg %q must be the last arg", aw, a.Name)
		}
		if !a.Required {
			requiredAfterOptional = true
		} else if requiredAfterOptional {
			return fmt.Errorf("%s: required arg %q cannot follow an optional arg", aw, a.Name)
		}
	}

	flagNames := set.New[string]()
	flagShorts := set.New[string]()
	for i, fl := range c.Flags {
		fw := fmt.Sprintf("%s.flags[%d]", where, i)
		if strings.TrimSpace(fl.Name) == "" {
			return fmt.Errorf("%s: name required", fw)
		}
		if !validFlagTypes.Contains(fl.Type) {
			return fmt.Errorf("%s: type %q must be one of string|bool|int|string-slice", fw, fl.Type)
		}
		if flagNames.Contains(fl.Name) {
			return fmt.Errorf("%s: duplicate flag name %q", fw, fl.Name)
		}
		flagNames.Add(fl.Name)
		if fl.Short != "" {
			if len(fl.Short) != 1 {
				return fmt.Errorf("%s: short %q must be a single character", fw, fl.Short)
			}
			if flagShorts.Contains(fl.Short) {
				return fmt.Errorf("%s: duplicate short %q", fw, fl.Short)
			}
			flagShorts.Add(fl.Short)
		}
		if strings.HasPrefix(fl.Name, "no-") {
			return fmt.Errorf("%s: flag name %q cannot start with \"no-\" (reserved for bool negation)", fw, fl.Name)
		}
	}
	for i, fl := range c.Flags {
		fw := fmt.Sprintf("%s.flags[%d]", where, i)
		for _, peer := range fl.Conflicts {
			if peer == fl.Name {
				return fmt.Errorf("%s: flag %q conflicts with itself", fw, fl.Name)
			}
			if !flagNames.Contains(peer) {
				return fmt.Errorf("%s: flag %q conflicts with unknown flag %q", fw, fl.Name, peer)
			}
		}
	}

	if c.Command.Defined() && c.Request.Defined() {
		return fmt.Errorf("%s: a <run> is either a command or a request, not both", where)
	}
	if err := validateRequest(c.Request, where+".request", transports); err != nil {
		return err
	}
	if c.Fields != nil && c.Format.Defined() {
		return fmt.Errorf("%s: use either <fields> or <format>, not both", where)
	}
	if c.Fields != nil && len(c.Commands) > 0 {
		return fmt.Errorf("%s: <fields> is only allowed on leaves (nodes with no subcommands)", where)
	}

	if err := validateDownloads(c, where, transports); err != nil {
		return err
	}

	if c.TML != nil {
		if len(c.Commands) > 0 {
			return fmt.Errorf("%s: <tml> is only allowed on leaves (nodes with no subcommands)", where)
		}
		if c.Fields != nil {
			return fmt.Errorf("%s: use either <tml> or <fields>, not both", where)
		}
		if err := validateTML(c.TML, where); err != nil {
			return err
		}
	}

	haveRun := inheritedRun || c.Command.Defined() || c.Request.Defined()

	// Leaf: must have something to do. A <download> leaf is that something —
	// the hand-off is the action, so it needs no run of its own.
	if len(c.Commands) == 0 {
		if !haveRun && len(c.Downloads) == 0 {
			return fmt.Errorf("%s: leaf has no command/request/download and no ancestor defines one", where)
		}
	}

	// `entry` and `steps` only make sense on leaves.
	if len(c.Entry) > 0 && len(c.Commands) > 0 {
		return fmt.Errorf("%s: `entry` is only allowed on leaves (nodes with no subcommands)", where)
	}
	if len(c.Steps) > 0 && len(c.Commands) > 0 {
		return fmt.Errorf("%s: `steps` is only allowed on leaves (nodes with no subcommands)", where)
	}
	if len(c.Preconditions) > 0 && len(c.Commands) > 0 {
		return fmt.Errorf("%s: `preconditions` is only allowed on leaves (nodes with no subcommands)", where)
	}
	stepNames := set.New[string]()
	for i, s := range c.Steps {
		sw := fmt.Sprintf("%s.steps[%d]", where, i)
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("%s: name required", sw)
		}
		if stepNames.Contains(s.Name) {
			return fmt.Errorf("%s: duplicate step name %q", sw, s.Name)
		}
		stepNames.Add(s.Name)
		if s.Command.Defined() && s.Request.Defined() {
			return fmt.Errorf("%s: a <run> is either a command or a request, not both", sw)
		}
		if err := validateRequest(s.Request, sw+".request", transports); err != nil {
			return err
		}
	}

	if c.Format.Defined() {
		fw := where + ".format"
		if c.Format.Inline != nil {
			if err := validateFormat(c.Format.Inline, fw); err != nil {
				return err
			}
		} else if c.Format.Name != "" {
			if _, ok := formats[c.Format.Name]; !ok {
				return fmt.Errorf("%s: references unknown format %q", fw, c.Format.Name)
			}
		}
	}

	childSeen := map[string]bool{}
	for i, child := range c.Commands {
		cw := fmt.Sprintf("%s.commands[%d]", where, i)
		if err := validateCommand(&child, cw, childSeen, haveRun, formats, transports); err != nil {
			return err
		}
	}
	return nil
}
