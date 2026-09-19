package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// Mock is the <mock> element on a leaf: a declarative stand-in for a real
// program. The leaf's argv arrives through passthrough mode, <input> names the
// parts of it that matter, <output> writes the files the caller expects to find
// afterwards, and <record> appends one line per invocation to a log.
//
// A build tool decides what to do next from the files on disk and their
// timestamps. So a mock compiler that writes its declared outputs satisfies
// make without compiling anything, and a whole toolchain can be stood up this
// way before one line of the real work exists.
//
// A leaf that declares <mock> and a <run> is a thin wrapper instead: the
// records and outputs happen, then the real program runs. That is how a
// compile_commands.json falls out of an ordinary build.
type Mock struct {
	Inputs  []MockInput  `json:"inputs,omitempty"`
	Outputs []MockOutput `json:"outputs,omitempty"`
	Records []MockRecord `json:"records,omitempty"`
	// Exit is a template for the exit code. Empty means 0, and a leaf with a
	// <run> ignores it in favour of the program's own code.
	Exit string `json:"exit,omitempty"`
	// Stdout and Stderr are templates written before the outputs land, for a
	// tool whose callers read its output rather than its files.
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

// MockInput names one part of the incoming argv. Match is a Go regular
// expression tested against each element of .rest in order, so the declaration
// reads as the shape of the command line rather than as an index into it.
//
// The resolved value lands at .mock.<name>: the first match, or every match
// when variadic= is set.
type MockInput struct {
	Name     string `json:"name"`
	Match    string `json:"match"`
	Variadic bool   `json:"variadic,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Default is a template used when nothing matches. It is the alternative to
	// required=, for an argument the real tool also defaults.
	Default string `json:"default,omitempty"`

	re *regexp.Regexp // compiled at load
}

// MockOutput is one file the mock writes. Path is a template, and the element's
// text content is the file's body — also a template, and empty by default,
// because a build tool reads a mock artifact's timestamp rather than its bytes.
//
// With over=, the declaration repeats per element of a list, exactly as a
// <download over=> does: the element's keys are promoted and the element itself
// is .item. One <output> therefore covers a compiler invoked with N sources.
type MockOutput struct {
	Over   string `json:"over,omitempty"`
	When   string `json:"when,omitempty"`
	Path   string `json:"path"`
	Text   string `json:"text,omitempty"`
	From   string `json:"from,omitempty"`
	Append bool   `json:"append,omitempty"`
	// Mode is an octal file mode. Empty takes 0644, and a mock that stands in
	// for a linker wants 0755.
	Mode string `json:"mode,omitempty"`
}

// MockRecord appends one line to a file per invocation. The default body is a
// compile_commands.json entry for this call, which is the reason the element
// exists: point every tool in a build at one record file, and the build writes
// its own compilation database on the way past.
type MockRecord struct {
	When string `json:"when,omitempty"`
	Path string `json:"path"`
	Text string `json:"text,omitempty"`
}

// defaultRecordText is one compile_commands.json entry. It is the default body
// of a <record>, so a mock toolchain produces a compilation database with no
// template written by hand. The JSON is built by the template rather than by
// Go, so an author who wants a different shape overrides the whole body.
const defaultRecordText = `{"directory":{{ .mock.cwd | toJson }},` +
	`"arguments":{{ .mock.argv | toJson }},` +
	`"file":{{ .mock.file | toJson }},` +
	`"output":{{ .mock.output | toJson }}}`

// buildMock reads the <mock> element. The regular expressions compile here, so
// a bad pattern is a load error rather than a surprise on the first invocation.
func buildMock(n *xnode) (*Mock, error) {
	if err := checkAttrs(n, "exit"); err != nil {
		return nil, err
	}
	m := &Mock{Exit: n.Attr("exit")}
	for _, child := range n.Children() {
		switch child.Name() {
		case "input":
			in, err := buildMockInput(child)
			if err != nil {
				return nil, err
			}
			m.Inputs = append(m.Inputs, in)
		case "output":
			out, err := buildMockOutput(child)
			if err != nil {
				return nil, err
			}
			m.Outputs = append(m.Outputs, out)
		case "record":
			rec, err := buildMockRecord(child)
			if err != nil {
				return nil, err
			}
			m.Records = append(m.Records, rec)
		case "stdout":
			body, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			m.Stdout = body
		case "stderr":
			body, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			m.Stderr = body
		default:
			return nil, fmt.Errorf("<mock>: unknown element <%s>", child.Name())
		}
	}
	return m, nil
}

func buildMockInput(n *xnode) (MockInput, error) {
	if err := checkAttrs(n, "name", "match", "variadic", "required", "default"); err != nil {
		return MockInput{}, err
	}
	in := MockInput{
		Name:     n.Attr("name"),
		Match:    n.Attr("match"),
		Variadic: n.Attr("variadic") == "true",
		Required: n.Attr("required") == "true",
		Default:  n.Attr("default"),
	}
	if strings.TrimSpace(in.Name) == "" {
		return MockInput{}, fmt.Errorf("<mock><input>: \"name\" is required")
	}
	if strings.TrimSpace(in.Match) == "" {
		return MockInput{}, fmt.Errorf("<mock><input name=%q>: \"match\" is required", in.Name)
	}
	re, err := regexp.Compile(in.Match)
	if err != nil {
		return MockInput{}, fmt.Errorf("<mock><input name=%q>: match %q: %w", in.Name, in.Match, err)
	}
	in.re = re
	if in.Required && in.Default != "" {
		return MockInput{}, fmt.Errorf("<mock><input name=%q>: required= and default= cannot both hold: a default is what makes a missing value legal", in.Name)
	}
	return in, nil
}

func buildMockOutput(n *xnode) (MockOutput, error) {
	if err := checkAttrs(n, "path", "when", "over", "from", "append", "mode"); err != nil {
		return MockOutput{}, err
	}
	body, err := compileContent(n)
	if err != nil {
		return MockOutput{}, err
	}
	out := MockOutput{
		Over:   n.Attr("over"),
		When:   n.Attr("when"),
		Path:   n.Attr("path"),
		From:   n.Attr("from"),
		Append: n.Attr("append") == "true",
		Mode:   n.Attr("mode"),
		Text:   body,
	}
	if strings.TrimSpace(out.Path) == "" {
		return MockOutput{}, fmt.Errorf("<mock><output>: \"path\" is required")
	}
	if out.From != "" && strings.TrimSpace(body) != "" {
		return MockOutput{}, fmt.Errorf("<mock><output path=%q>: from= and a text body cannot both hold: one of them is the file's contents", out.Path)
	}
	if err := checkMockMode(out.Mode, out.Path); err != nil {
		return MockOutput{}, err
	}
	return out, nil
}

func buildMockRecord(n *xnode) (MockRecord, error) {
	if err := checkAttrs(n, "path", "when"); err != nil {
		return MockRecord{}, err
	}
	body, err := compileContent(n)
	if err != nil {
		return MockRecord{}, err
	}
	rec := MockRecord{Path: n.Attr("path"), When: n.Attr("when"), Text: body}
	if strings.TrimSpace(rec.Path) == "" {
		return MockRecord{}, fmt.Errorf("<mock><record>: \"path\" is required")
	}
	if strings.TrimSpace(rec.Text) == "" {
		rec.Text = defaultRecordText
	}
	return rec, nil
}

// checkMockMode rejects a mode that is not octal. A mode is the one attribute
// here whose typo is silent: 755 without the leading zero still parses, and
// 0o755 does not parse at all.
func checkMockMode(mode, path string) error {
	if mode == "" {
		return nil
	}
	if strings.Contains(mode, "{{") {
		return nil
	}
	if _, err := parseFileMode(mode); err != nil {
		return fmt.Errorf("<mock><output path=%q>: mode %q must be octal, such as 0644 or 0755", path, mode)
	}
	return nil
}

// validateMock is the meaning check, after every element has parsed. A mock
// stands in for a program, and a program that writes nothing and says nothing
// is a leaf that silently does nothing at all.
func validateMock(c *Command, where string) error {
	m := c.Mock
	if m == nil {
		return nil
	}
	if len(c.Commands) > 0 {
		return fmt.Errorf("%s: <mock> is only allowed on leaves", where)
	}
	if c.Request.Defined() {
		return fmt.Errorf("%s: <mock> and <request> cannot both hold: a mock stands in for a program, not for an API", where)
	}
	if len(c.Downloads) > 0 {
		return fmt.Errorf("%s: <mock> and <download> cannot both hold: each one is the leaf's whole action", where)
	}
	if len(m.Outputs) == 0 && len(m.Records) == 0 && m.Stdout == "" && m.Stderr == "" {
		return fmt.Errorf("%s: <mock> declares no <output>, <record>, <stdout> or <stderr>, so it stands in for nothing", where)
	}
	names := set.New[string]()
	for i, in := range m.Inputs {
		if names.Contains(in.Name) {
			return fmt.Errorf("%s.mock.inputs[%d]: duplicate input name %q", where, i, in.Name)
		}
		names.Add(in.Name)
		if reservedMockNames.Contains(in.Name) {
			return fmt.Errorf("%s.mock.inputs[%d]: name %q is reserved: .mock.%s is what the engine puts there", where, i, in.Name, in.Name)
		}
	}
	return nil
}
