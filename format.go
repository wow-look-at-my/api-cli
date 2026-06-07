package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// defaultFormatCap is the byte threshold above which captureExecCapped flushes
// to streaming and skips the format step. 32 MiB.
const defaultFormatCap = 32 << 20

// userVerdict is the user-side decision about formatting. The author-side is
// computed separately by rendering format.When; output is formatted iff both
// sides agree.
type userVerdict int

const (
	userYes     userVerdict = iota // user wants formatting (default)
	userNo                         // user opted out (--no-format / NO_FORMAT / --format=raw)
	userAlways                     // user wants formatting and asks us to "lie" about TTY
)

// resolveFormat returns the effective Format for a leaf, looking up named
// references in the registry. nil means no format applies.
func resolveFormat(ref *FormatRef, formats map[string]*Format) *Format {
	if !ref.Defined() {
		return nil
	}
	if ref.Inline != nil {
		return ref.Inline
	}
	return formats[ref.Name]
}

// userVerdictFromFlags consults the persistent flags and env vars in
// precedence order:
//  1. --no-format       -> userNo
//  2. --format=<value>  -> raw=>userNo, always=>userAlways, auto=>(env or default)
//  3. NO_FORMAT         -> userNo (any non-empty value, NO_COLOR-style)
//  4. API_CLI_FORMAT    -> raw / always / auto
//  5. default           -> userYes
func userVerdictFromFlags(c *cobra.Command) userVerdict {
	root := c.Root().PersistentFlags()
	if no, _ := root.GetBool("no-format"); no {
		return userNo
	}
	flag, _ := root.GetString("format")
	switch flag {
	case "raw":
		return userNo
	case "always":
		return userAlways
	case "auto", "":
		// fall through to env
	}
	if v := os.Getenv("NO_FORMAT"); v != "" {
		return userNo
	}
	switch os.Getenv("API_CLI_FORMAT") {
	case "raw":
		return userNo
	case "always":
		return userAlways
	}
	return userYes
}

// stdoutTTY reports whether execStdout is a terminal. Non-*os.File writers
// (e.g. bytes.Buffer in tests) return false — exactly what tests want.
func stdoutTTY() (bool, int) {
	f, ok := execStdout.(*os.File)
	if !ok {
		return false, 0
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return false, 0
	}
	w, _, err := term.GetSize(fd)
	if err != nil || w <= 0 {
		w = 80
	}
	return true, w
}

// formatContext builds the data passed to format predicates and view
// templates. parsed is the decoded child stdout (nil before exec for
// when-predicate evaluation). data is the leaf's full data context (.arg,
// .flag, .env, .var, .entry, .result).
func formatContext(parsed any, data map[string]any, isTTY bool, width int) map[string]any {
	ctx := map[string]any{
		"data":  parsed,
		"tty":   isTTY,
		"width": width,
	}
	for k, v := range data {
		ctx[k] = v
	}
	return ctx
}

// renderPredicate renders tmpl against ctx and reports whether the trimmed
// output is truthy. Empty tmpl defaults to "{{.tty}}". Falsy values: "",
// "false", "0", "no" (case-insensitive). Caches results across calls within
// the same invocation by (tmpl-source, ctx-pointer).
func renderPredicate(tmpl string, ctx map[string]any, cache map[predicateKey]bool) (bool, error) {
	if tmpl == "" {
		tmpl = "{{.tty}}"
	}
	key := predicateKey{tmpl: tmpl, ctx: ctxIdentity(ctx)}
	if cache != nil {
		if v, ok := cache[key]; ok {
			return v, nil
		}
	}
	out, err := renderString(tmpl, ctx)
	if err != nil {
		return false, err
	}
	v := isTruthy(out)
	if cache != nil {
		cache[key] = v
	}
	return v, nil
}

type predicateKey struct {
	tmpl string
	ctx  uintptr
}

// ctxIdentity returns a stable identity for a context map. The map header's
// pointer is stable for the lifetime of the map; we don't mutate ctx after
// passing it in, so this is safe for one-invocation caching.
func ctxIdentity(m map[string]any) uintptr {
	if m == nil {
		return 0
	}
	return reflect.ValueOf(m).Pointer()
}

func isTruthy(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	switch strings.ToLower(t) {
	case "false", "0", "no":
		return false
	}
	return true
}

// parseInput interprets the captured child stdout per the format's Input
// setting.
func parseInput(s, mode string) any {
	switch mode {
	case "lines":
		s = strings.TrimRight(s, "\n")
		if s == "" {
			return []string{}
		}
		return strings.Split(s, "\n")
	case "raw":
		return strings.TrimRight(s, "\n")
	default: // "json" or empty
		return parseResult(s)
	}
}

// selectView picks the view to render. Selection rules:
//  1. If viewFlag is non-empty, return the named view (or error).
//  2. Else first view whose When predicate is truthy.
//  3. Else first view with Default true.
//  4. Else views[0].
func selectView(views []View, ctx map[string]any, viewFlag string, cache map[predicateKey]bool) (*View, error) {
	if viewFlag != "" {
		for i := range views {
			if views[i].Name == viewFlag {
				logVerbose("format: selected view %q (explicit --view flag)", views[i].Name)
				return &views[i], nil
			}
		}
		return nil, fmt.Errorf("unknown view %q", viewFlag)
	}
	for i := range views {
		if views[i].When == "" {
			continue
		}
		ok, err := renderPredicate(views[i].When, ctx, cache)
		if err != nil {
			return nil, fmt.Errorf("view %q: %w", views[i].Name, err)
		}
		logDebug("format: view %q when=%q => %v", views[i].Name, views[i].When, ok)
		if ok {
			logVerbose("format: selected view %q (when predicate)", views[i].Name)
			return &views[i], nil
		}
	}
	for i := range views {
		if views[i].Default {
			logVerbose("format: selected view %q (default)", views[i].Name)
			return &views[i], nil
		}
	}
	logVerbose("format: selected view %q (first)", views[0].Name)
	return &views[0], nil
}

// execLeaf decides how to run a leaf (shell/argv command or HTTP request) and
// how to present its output (the <fields> auto-formatter, a legacy <format>
// view, or raw streaming).
//
// Formatting is suppressed when the user opts out (--no-format / --format=raw /
// NO_FORMAT). A <fields> declaration otherwise always formats. A legacy
// <format> additionally requires its author `when` predicate to be truthy.
func execLeaf(c *cobra.Command, cmdTmpl *Cmd, request *Request, cwd, stdin string, data map[string]any, fields *Fields, formatRef *FormatRef, formats map[string]*Format) (int, error) {
	verdict := userVerdictFromFlags(c)

	streamRaw := func() int {
		if request.Defined() {
			return streamRequest(request, data)
		}
		return doExec(cmdTmpl, cwd, stdin, data)
	}

	if verdict == userNo {
		logVerbose("format: user opted out, streaming raw")
		return streamRaw(), nil
	}

	// --as forces a representation even when the leaf declared no <fields>:
	// project nothing and let the data shape (or the chosen sink) decide.
	if fields == nil {
		if sink, _ := c.Root().PersistentFlags().GetString("as"); strings.TrimSpace(sink) != "" {
			fields = &Fields{}
		}
	}

	if fields != nil {
		logVerbose("format: applying <fields> auto-formatter")
		return runFieldsFormatted(c, cmdTmpl, request, cwd, stdin, data, fields, verdict), nil
	}

	effFmt := resolveFormat(formatRef, formats)
	if effFmt == nil {
		logDebug("format: none configured, streaming raw")
		return streamRaw(), nil
	}

	isTTY, width := stdoutTTY()
	if verdict == userAlways {
		isTTY = true
		if width == 0 {
			width = 80
		}
	}
	cache := map[predicateKey]bool{}
	preCtx := formatContext(nil, data, isTTY, width)
	authorOK, err := renderPredicate(effFmt.When, preCtx, cache)
	if err != nil {
		return 1, fmt.Errorf("format when: %w", err)
	}
	logVerbose("format: author when=%q => %v", effFmt.When, authorOK)
	if !authorOK {
		logVerbose("format: author predicate false, streaming raw")
		return streamRaw(), nil
	}

	logVerbose("format: applying format with %d views", len(effFmt.Views))
	return runFormatted(c, cmdTmpl, request, cwd, stdin, data, effFmt, verdict), nil
}

// captureRun captures a leaf's output. For a request it performs the HTTP call
// (the body is always buffered); for a command it uses the capped capture path.
func captureRun(cmdTmpl *Cmd, request *Request, cwd, stdin string, data map[string]any) (out string, overflowed bool, code int) {
	if request.Defined() {
		o, c := runRequest(request, data, execStderr)
		return o, false, c
	}
	return captureExecCapped(cmdTmpl, cwd, stdin, data, defaultFormatCap)
}

// streamRequest performs a request and writes its output straight to stdout.
func streamRequest(request *Request, data map[string]any) int {
	out, code := runRequest(request, data, execStderr)
	if code != 0 {
		return code
	}
	fmt.Fprint(execStdout, out)
	if out != "" && !strings.HasSuffix(out, "\n") {
		fmt.Fprintln(execStdout)
	}
	return 0
}

// runFieldsFormatted captures the leaf's JSON output and renders it through the
// <fields> auto-formatter.
func runFieldsFormatted(c *cobra.Command, cmdTmpl *Cmd, request *Request, cwd, stdin string, data map[string]any, fields *Fields, verdict userVerdict) int {
	out, overflowed, code := captureRun(cmdTmpl, request, cwd, stdin, data)
	if overflowed {
		logVerbose("format: output overflowed cap, streamed raw")
		return code
	}
	if code != 0 {
		if out != "" {
			fmt.Fprint(execStderr, out)
		}
		return code
	}

	isTTY, width := stdoutTTY()
	if verdict == userAlways {
		isTTY = true
		if width == 0 {
			width = 80
		}
	}
	parsed := parseInput(out, "json")
	ctx := formatContext(parsed, data, isTTY, width)
	sink, _ := c.Root().PersistentFlags().GetString("as")
	dropWidth := 0
	if isTTY {
		dropWidth = width
	}
	rendered, err := renderFields(fields, parsed, ctx, strings.TrimSpace(sink), dropWidth)
	if err != nil {
		fmt.Fprintln(execStderr, "error:", err)
		return 1
	}
	fmt.Fprint(execStdout, rendered)
	return 0
}

// runFormatted executes the leaf via captureRun and renders the captured output
// through the selected legacy view.
func runFormatted(
	c *cobra.Command,
	cmdTmpl *Cmd,
	request *Request,
	cwd, stdin string,
	data map[string]any,
	f *Format,
	verdict userVerdict,
) int {
	out, overflowed, code := captureRun(cmdTmpl, request, cwd, stdin, data)
	logDebug("format: captured %d bytes, overflowed=%v, code=%d", len(out), overflowed, code)
	if overflowed {
		logVerbose("format: output overflowed cap, streamed raw")
		return code
	}
	if code != 0 {
		fmt.Fprint(execStderr, out)
		return code
	}

	parsed := parseInput(out, f.Input)
	logDebug("format: input mode=%q parsed type=%T", f.Input, parsed)
	isTTY, width := stdoutTTY()
	if verdict == userAlways {
		isTTY = true
		if width == 0 {
			width = 80
		}
	}
	ctx := formatContext(parsed, data, isTTY, width)

	cache := map[predicateKey]bool{}
	viewFlag, _ := c.Root().PersistentFlags().GetString("view")
	view, err := selectView(f.Views, ctx, viewFlag, cache)
	if err != nil {
		fmt.Fprintln(execStderr, "error:", err)
		return 1
	}

	rendered, err := renderString(view.Template, ctx)
	if err != nil {
		fmt.Fprintln(execStderr, "error: render view:", err)
		return 1
	}
	logDebug("format: rendered view %q (%d bytes)", view.Name, len(rendered))
	fmt.Fprint(execStdout, rendered)
	return 0
}
