package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// reservedMockNames are the keys the engine itself puts on .mock, so an
// <input> may not take one.
var reservedMockNames = set.Of("argv", "cwd", "file", "output", "outputs")

// defaultMockMode is the mode of a mock artifact. A build tool reads the
// timestamp, so the bytes and the bits both only have to be plausible.
const defaultMockMode fs.FileMode = 0o644

// mockFile is one planned write: the path rendered, the body decided, and
// nothing touched on disk yet. Planning every output before writing any is what
// lets a <record> name the files this call is about to produce.
type mockFile struct {
	path   string
	body   string
	from   string
	append bool
	mode   fs.FileMode
}

// resolveMockInputs walks .rest with each <input>'s pattern and publishes the
// result at .mock. It runs before anything is written, because an output path
// is usually derived from an input.
func resolveMockInputs(m *Mock, data map[string]any) error {
	rest, _ := asList(data["rest"])
	argv := make([]string, 0, len(rest))
	for _, el := range rest {
		argv = append(argv, fmt.Sprintf("%v", el))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("mock: working directory: %w", err)
	}

	mock := map[string]any{"argv": argv, "cwd": cwd}
	data["mock"] = mock

	var first string
	for i := range m.Inputs {
		in := &m.Inputs[i]
		var hits []string
		for _, a := range argv {
			if in.re.MatchString(a) {
				hits = append(hits, a)
				if !in.Variadic {
					break
				}
			}
		}

		if in.Variadic {
			if len(hits) == 0 && in.Required {
				return fmt.Errorf("mock: input %q matched nothing in %v (match=%q)", in.Name, argv, in.Match)
			}
			mock[in.Name] = hits
			if first == "" && len(hits) > 0 {
				first = hits[0]
			}
			continue
		}

		value := ""
		switch {
		case len(hits) > 0:
			value = hits[0]
		case in.Required:
			return fmt.Errorf("mock: input %q matched nothing in %v (match=%q)", in.Name, argv, in.Match)
		case in.Default != "":
			// The default renders against .mock as it stands, so a later input
			// can fall back to one resolved before it.
			if value, err = renderString(in.Default, data); err != nil {
				return fmt.Errorf("mock: input %q: render default: %w", in.Name, err)
			}
		}
		mock[in.Name] = value
		if first == "" {
			first = value
		}
	}

	// .mock.file is the first input that resolved to something, which is the
	// source file in every compile-tool shape this exists for.
	mock["file"] = first
	logVerbose("mock: inputs %s", jsonCompact(mock))
	return nil
}

// planMockOutputs renders every <output> into a concrete write. It also
// publishes .mock.outputs and .mock.output, so a <record> can name the
// artifacts of this call without repeating their path templates.
func planMockOutputs(m *Mock, data map[string]any) ([]mockFile, error) {
	var files []mockFile
	claimed := map[string]bool{}

	for i := range m.Outputs {
		o := &m.Outputs[i]
		ok, err := mockWhen(o.When, data)
		if err != nil {
			return nil, fmt.Errorf("mock: output[%d]: %w", i, err)
		}
		if !ok {
			continue
		}
		records, err := mockRecordsOf(o.Over, data, i)
		if err != nil {
			return nil, err
		}
		for _, ctx := range records {
			f, err := planMockOutput(o, ctx, i)
			if err != nil {
				return nil, err
			}
			// Two records rendering one path is an over= whose path template
			// forgot to vary. Caught here, rather than after N writes have
			// landed on top of each other.
			if !f.append {
				if claimed[f.path] {
					return nil, fmt.Errorf("mock: output[%d]: two records both write %s; give path= something that varies per record", i, f.path)
				}
				claimed[f.path] = true
			}
			files = append(files, f)
		}
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	mock, _ := data["mock"].(map[string]any)
	if mock != nil {
		mock["outputs"] = paths
		mock["output"] = ""
		if len(paths) > 0 {
			mock["output"] = paths[0]
		}
	}
	return files, nil
}

func planMockOutput(o *MockOutput, ctx map[string]any, idx int) (mockFile, error) {
	path, err := renderString(o.Path, ctx)
	if err != nil {
		return mockFile{}, fmt.Errorf("mock: output[%d]: render path: %w", idx, err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return mockFile{}, fmt.Errorf("mock: output[%d]: path=%q rendered empty", idx, o.Path)
	}

	f := mockFile{path: path, append: o.Append, mode: defaultMockMode}

	if o.Mode != "" {
		rendered, err := renderString(o.Mode, ctx)
		if err != nil {
			return mockFile{}, fmt.Errorf("mock: output[%d]: render mode: %w", idx, err)
		}
		if f.mode, err = parseFileMode(rendered); err != nil {
			return mockFile{}, fmt.Errorf("mock: output[%d]: mode %q must be octal, such as 0644 or 0755", idx, rendered)
		}
	}

	if o.From != "" {
		if f.from, err = renderString(o.From, ctx); err != nil {
			return mockFile{}, fmt.Errorf("mock: output[%d]: render from: %w", idx, err)
		}
		if strings.TrimSpace(f.from) == "" {
			return mockFile{}, fmt.Errorf("mock: output[%d]: from=%q rendered empty", idx, o.From)
		}
		return f, nil
	}

	if f.body, err = renderString(o.Text, ctx); err != nil {
		return mockFile{}, fmt.Errorf("mock: output[%d]: render body: %w", idx, err)
	}
	return f, nil
}

// writeMockFiles performs the planned writes. A parent directory is created,
// because a real compiler writing into an existing build tree finds one there.
func writeMockFiles(files []mockFile) error {
	for _, f := range files {
		if dir := filepath.Dir(f.path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("mock: %s: %w", f.path, err)
			}
		}
		if f.from != "" {
			if err := copyMockFile(f); err != nil {
				return err
			}
			continue
		}
		if err := writeMockBody(f); err != nil {
			return err
		}
		logVerbose("mock: wrote %s (%d bytes, mode %04o)", f.path, len(f.body), f.mode)
	}
	return nil
}

func writeMockBody(f mockFile) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if f.append {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	fh, err := os.OpenFile(f.path, flags, f.mode)
	if err != nil {
		return fmt.Errorf("mock: %s: %w", f.path, err)
	}
	defer fh.Close()
	if _, err := io.WriteString(fh, f.body); err != nil {
		return fmt.Errorf("mock: %s: %w", f.path, err)
	}
	// A file that already existed keeps its old mode through O_CREATE, and a
	// linker stand-in that produces a non-executable binary breaks the next
	// step of the build.
	if err := os.Chmod(f.path, f.mode); err != nil {
		return fmt.Errorf("mock: %s: %w", f.path, err)
	}
	return nil
}

func copyMockFile(f mockFile) error {
	src, err := os.Open(f.from)
	if err != nil {
		return fmt.Errorf("mock: %s: from=%s: %w", f.path, f.from, err)
	}
	defer src.Close()

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if f.append {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	dst, err := os.OpenFile(f.path, flags, f.mode)
	if err != nil {
		return fmt.Errorf("mock: %s: %w", f.path, err)
	}
	defer dst.Close()

	n, err := io.Copy(dst, src)
	if err != nil {
		return fmt.Errorf("mock: %s: %w", f.path, err)
	}
	if err := os.Chmod(f.path, f.mode); err != nil {
		return fmt.Errorf("mock: %s: %w", f.path, err)
	}
	logVerbose("mock: copied %s -> %s (%d bytes)", f.from, f.path, n)
	return nil
}

// appendMockRecords writes one line per <record>. The append is the whole
// point: every tool in a parallel build points at one file, and each call adds
// its own line without reading what is already there.
func appendMockRecords(m *Mock, data map[string]any) error {
	for i := range m.Records {
		r := &m.Records[i]
		ok, err := mockWhen(r.When, data)
		if err != nil {
			return fmt.Errorf("mock: record[%d]: %w", i, err)
		}
		if !ok {
			continue
		}
		path, err := renderString(r.Path, data)
		if err != nil {
			return fmt.Errorf("mock: record[%d]: render path: %w", i, err)
		}
		path = strings.TrimSpace(path)
		if path == "" {
			return fmt.Errorf("mock: record[%d]: path=%q rendered empty", i, r.Path)
		}
		line, err := renderString(r.Text, data)
		if err != nil {
			return fmt.Errorf("mock: record[%d]: render body: %w", i, err)
		}
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("mock: record[%d]: %s: %w", i, path, err)
			}
		}
		fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, defaultMockMode)
		if err != nil {
			return fmt.Errorf("mock: record[%d]: %s: %w", i, path, err)
		}
		_, werr := io.WriteString(fh, strings.TrimRight(line, "\n")+"\n")
		cerr := fh.Close()
		if werr != nil {
			return fmt.Errorf("mock: record[%d]: %s: %w", i, path, werr)
		}
		if cerr != nil {
			return fmt.Errorf("mock: record[%d]: %s: %w", i, path, cerr)
		}
		logVerbose("mock: recorded to %s", path)
	}
	return nil
}

// runMock is the leaf's action. It resolves the inputs, plans and writes the
// outputs, appends the records, then emits whatever the mock says. The return
// is the exit code the process takes when no <run> follows.
func runMock(m *Mock, data map[string]any, out, errw io.Writer) (int, error) {
	if err := resolveMockInputs(m, data); err != nil {
		return 1, err
	}
	files, err := planMockOutputs(m, data)
	if err != nil {
		return 1, err
	}
	// The records go first: a wrapper around a real tool must log the call even
	// when the tool that follows fails, because a failed compile is exactly the
	// one somebody wants the command line of.
	if err := appendMockRecords(m, data); err != nil {
		return 1, err
	}
	if err := writeMockFiles(files); err != nil {
		return 1, err
	}
	if err := emitMockText(m.Stdout, data, out); err != nil {
		return 1, err
	}
	if err := emitMockText(m.Stderr, data, errw); err != nil {
		return 1, err
	}
	return mockExitCode(m, data)
}

func emitMockText(tmpl string, data map[string]any, w io.Writer) error {
	if tmpl == "" {
		return nil
	}
	out, err := renderString(tmpl, data)
	if err != nil {
		return fmt.Errorf("mock: render output text: %w", err)
	}
	_, err = io.WriteString(w, out)
	return err
}

func mockExitCode(m *Mock, data map[string]any) (int, error) {
	if m.Exit == "" {
		return 0, nil
	}
	out, err := renderString(m.Exit, data)
	if err != nil {
		return 1, fmt.Errorf("mock: render exit: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	code, err := strconv.Atoi(out)
	if err != nil {
		return 1, fmt.Errorf("mock: exit=%q rendered %q, which is not a number", m.Exit, out)
	}
	return code, nil
}

// mockWhen renders a predicate. An empty one holds, as it does everywhere else
// in this config language.
func mockWhen(when string, data map[string]any) (bool, error) {
	if when == "" {
		return true, nil
	}
	out, err := renderString(when, data)
	if err != nil {
		return false, fmt.Errorf("render when: %w", err)
	}
	return isTruthy(out), nil
}

// mockRecordsOf expands an over= into one context per element, the same
// contract a <download over=> carries: the element's keys are promoted and the
// element itself is .item.
func mockRecordsOf(over string, data map[string]any, idx int) ([]map[string]any, error) {
	if over == "" {
		return []map[string]any{data}, nil
	}
	src, ok, err := overSource(data, over)
	if err != nil {
		return nil, fmt.Errorf("mock: output[%d]: %w", idx, err)
	}
	if !ok || src == nil {
		return nil, fmt.Errorf("mock: output[%d]: over=%q resolved to nothing", idx, over)
	}
	list, ok := asList(src)
	if !ok {
		return []map[string]any{promoteCtx(data, src, "item")}, nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, el := range list {
		out = append(out, promoteCtx(data, el, "item"))
	}
	logVerbose("mock: output[%d]: over=%q expanded to %d records", idx, over, len(out))
	return out, nil
}

// parseFileMode reads an octal mode. A leading 0 is optional, because 644 is
// how a Makefile author writes it.
func parseFileMode(s string) (fs.FileMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultMockMode, nil
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0o"), 8, 32)
	if err != nil {
		return 0, err
	}
	return fs.FileMode(n), nil
}
