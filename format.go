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
		if ok {
			return &views[i], nil
		}
	}
	for i := range views {
		if views[i].Default {
			return &views[i], nil
		}
	}
	return &views[0], nil
}

// execLeaf decides whether to run the leaf via the streaming fast path or the
// captured-and-formatted path. AND semantics: format applies iff (effective
// format exists) AND (user verdict != no) AND (author predicate truthy).
func execLeaf(c *cobra.Command, cmdTmpl *Cmd, cwd, stdin string, data map[string]any, formatRef *FormatRef, formats map[string]*Format) (int, error) {
	effFmt := resolveFormat(formatRef, formats)
	if effFmt == nil {
		return doExec(cmdTmpl, cwd, stdin, data), nil
	}
	verdict := userVerdictFromFlags(c)
	if verdict == userNo {
		return doExec(cmdTmpl, cwd, stdin, data), nil
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
	if !authorOK {
		return doExec(cmdTmpl, cwd, stdin, data), nil
	}

	return runFormatted(c, cmdTmpl, cwd, stdin, data, effFmt, verdict), nil
}

// runFormatted executes the leaf command via captureExecCapped and renders the
// captured output through the selected view. AND semantics: must be called
// only after both author and user sides have agreed.
func runFormatted(
	c *cobra.Command,
	cmdTmpl *Cmd,
	cwd, stdin string,
	data map[string]any,
	f *Format,
	verdict userVerdict,
) int {
	out, overflowed, code := captureExecCapped(cmdTmpl, cwd, stdin, data, defaultFormatCap)
	if overflowed {
		// Output already streamed transparently.
		return code
	}
	if code != 0 {
		fmt.Fprint(execStderr, out)
		return code
	}

	parsed := parseInput(out, f.Input)
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
	fmt.Fprint(execStdout, rendered)
	return 0
}
