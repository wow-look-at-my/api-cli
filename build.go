package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// exitCode is set by a leaf's RunE to the exit status of the child process.
// main reads it after rootCmd.Execute() returns.
var exitCode int

var confirmYes = regexp.MustCompile(`^[yY]([eE][sS])?$`)

var isInteractive = func() bool {
	f, ok := execStdin.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// buildCommand turns a Command node into a cobra.Command, wiring up args,
// flags, and subcommands. inheritedVars flow down the tree (child overrides
// parent on key collision). inheritedCmd is the closest-ancestor command
// template; the node's own command, if set, overrides it for this subtree.
// inheritedCwd is the closest-ancestor working-directory template; the node's
// own cwd, if non-empty, overrides it for this subtree. inheritedStdin is the
// closest-ancestor stdin template; the node's own stdin, if non-empty,
// overrides it for this subtree. inheritedConfirm is the closest-ancestor
// confirm template; the node's own confirm, if non-empty, overrides it.
// inheritedFormat is the closest-ancestor format reference; the node's own
// format, if set, overrides it. formats is the top-level format registry used
// to resolve named references.
func buildCommand(node Command, inheritedVars map[string]any, inheritedCmd *Cmd, inheritedRequest *Request, inheritedCwd, inheritedStdin, inheritedConfirm string, inheritedFormat *FormatRef, formats map[string]*Format) *cobra.Command {
	useStr := node.Name
	requiredArgs := 0
	hasVariadic := false
	for _, a := range node.Args {
		token := a.Name
		if a.Variadic {
			token += "..."
			hasVariadic = true
		}
		if a.Required {
			useStr += " <" + token + ">"
			requiredArgs++
		} else {
			useStr += " [" + token + "]"
		}
	}

	cmd := &cobra.Command{
		Use:          useStr,
		Short:        node.Description,
		SilenceUsage: true,
	}

	if node.Passthrough {
		cmd.Args = cobra.ArbitraryArgs
	} else {
		var count cobra.PositionalArgs
		switch total := len(node.Args); {
		case total == 0 && node.Runnable:
			// A runnable node with no args takes none: an unmatched positional is
			// a mistyped subcommand, and cobra says so.
			count = cobra.NoArgs
		case total == 0:
			count = nil
		case hasVariadic:
			count = cobra.MinimumNArgs(requiredArgs)
		case requiredArgs == total:
			count = cobra.ExactArgs(total)
		default:
			count = cobra.RangeArgs(requiredArgs, total)
		}
		if count != nil {
			cmd.Args = chainArgs(count, matchArgPatterns(node, argPatterns(node)))
		}

		for _, f := range node.Flags {
			registerFlag(cmd, f)
		}
		registerConflicts(cmd, node.Flags)
	}

	// Resolve effective vars, run (command or request), cwd, stdin, and format
	// for this subtree. A node's <run> overrides the inherited one of either
	// kind: defining a command clears an inherited request and vice versa, so
	// the closest ancestor with any <run> wins.
	effectiveVars := mergeVars(inheritedVars, node.Vars)
	effectiveCmd := inheritedCmd
	effectiveRequest := inheritedRequest
	if node.Request.Defined() {
		effectiveRequest = node.Request
		effectiveCmd = nil
	} else if node.Command.Defined() {
		effectiveCmd = node.Command
		effectiveRequest = nil
	}
	effectiveCwd := inheritedCwd
	if node.Cwd != "" {
		effectiveCwd = node.Cwd
	}
	effectiveStdin := inheritedStdin
	if node.Stdin != "" {
		effectiveStdin = node.Stdin
	}
	effectiveConfirm := inheritedConfirm
	if node.Confirm != "" {
		effectiveConfirm = node.Confirm
	}
	effectiveFormat := inheritedFormat
	if node.Format.Defined() {
		effectiveFormat = node.Format
	}

	// A leaf executes, and so does a parent that declares runnable=. Cobra hands
	// this node the invocation only when no subcommand name matched.
	if node.executes() {
		nodeCopy := node
		leafVars := effectiveVars
		leafCmd := effectiveCmd
		leafRequest := effectiveRequest
		leafCwd := effectiveCwd
		leafStdin := effectiveStdin
		leafConfirm := effectiveConfirm
		leafFormat := effectiveFormat
		leafFormats := formats
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return runLeaf(c, nodeCopy, args, leafVars, leafCmd, leafRequest, leafCwd, leafStdin, leafConfirm, leafFormat, leafFormats)
		}
	}

	for _, child := range node.Commands {
		cmd.AddCommand(buildCommand(child, effectiveVars, effectiveCmd, effectiveRequest, effectiveCwd, effectiveStdin, effectiveConfirm, effectiveFormat, formats))
	}

	return cmd
}

// runLeaf is the per-invocation body for every leaf.
//
// Stages:
//  1. Assemble args, flags, env — the base template context.
//  2. Render the merged vars against the base context to produce .var.
//  3. Execute each step in order, capturing its stdout into .result.<name>.
//     Each step's entry template is rendered against the current context
//     (including .result.* from prior steps) before the step runs.
//  4. Render the leaf's own entry against the full context (including
//     .result.*) to produce .entry.
//  5. Render the effective command template against the full context and
//     execute it, streaming output to the user.
//  6. If more than one command was executed and --quiet is not set, print
//     the execution count to stderr.
//
// cwdTmpl is the effective working-directory template for this leaf; an empty
// string means "use the calling process's cwd". Each step inherits cwdTmpl
// unless the step itself sets `cwd`. The cwd template is rendered fresh per
// execution against the current data context (so .result and .entry are
// available where appropriate).
//
// stdinTmpl is the effective stdin template for this leaf; an empty string
// means "inherit the parent process's stdin". Each step inherits stdinTmpl
// unless the step itself sets `stdin`. The stdin template is rendered fresh
// per execution against the current data context.
func runLeaf(c *cobra.Command, node Command, args []string, vars map[string]any, cmdTmpl *Cmd, request *Request, cwdTmpl, stdinTmpl, confirmTmpl string, formatRef *FormatRef, formats map[string]*Format) error {
	verboseMode, _ = c.Root().PersistentFlags().GetBool("verbose")
	dbg, _ := c.Root().PersistentFlags().GetBool("debug")
	if dbg {
		debugMode = true
		verboseMode = true
	}

	var data map[string]any
	var err error

	if node.Passthrough {
		flagMap, rest := passthroughParse(args, node.Flags)
		base := map[string]any{"arg": map[string]any{}, "env": envMap(), "rest": rest}
		data, err = resolveContext(vars, base, func(map[string]any) (map[string]any, error) {
			return flagMap, nil
		})
	} else {
		var argMap map[string]any
		if argMap, err = gatherArgs(node, args); err != nil {
			return err
		}
		base := map[string]any{"arg": argMap, "env": envMap()}
		data, err = resolveContext(vars, base, func(preFlag map[string]any) (map[string]any, error) {
			return gatherFlags(c, node, preFlag)
		})
	}
	if err != nil {
		return err
	}

	logVerbose("leaf %q: starting", node.Name)
	logDebug("leaf %q: data context: %s", node.Name, jsonCompact(data))

	for i, p := range node.Preconditions {
		msg, perr := renderString(p, data)
		if perr != nil {
			return fmt.Errorf("precondition[%d]: %w", i, perr)
		}
		msg = strings.TrimSpace(msg)
		logVerbose("precondition[%d]: %q => %q (pass=%v)", i, p, msg, msg == "")
		if msg != "" {
			fmt.Fprintln(execStderr, "error:", msg)
			exitCode = 1
			return nil
		}
	}

	if confirmTmpl != "" {
		msg, cerr := renderString(confirmTmpl, data)
		if cerr != nil {
			return fmt.Errorf("render confirm: %w", cerr)
		}
		msg = strings.TrimSpace(msg)
		logDebug("confirm: template=%q rendered=%q", confirmTmpl, msg)
		if msg != "" {
			yes, _ := c.Root().PersistentFlags().GetBool("yes")
			if !yes {
				if !isInteractive() {
					fmt.Fprintln(execStderr, "error: refusing to run without confirmation; pass --yes")
					exitCode = 1
					return nil
				}
				fmt.Fprintf(execStderr, "%s [y/N] ", msg)
				scanner := bufio.NewScanner(execStdin)
				if !scanner.Scan() {
					exitCode = 1
					return nil
				}
				if !confirmYes.MatchString(strings.TrimSpace(scanner.Text())) {
					exitCode = 1
					return nil
				}
			}
		}
	}

	// A <download> leaf takes over the output channels before its steps run:
	// the steps are what work the URL out, so their output belongs in the same
	// log region as the transfers they feed.
	var session *downloadSession
	if len(node.Downloads) > 0 {
		session = startDownloadSession(c)
		defer session.close()
	}

	resultMap := map[string]any{}
	data["result"] = resultMap

	oc, err := runSteps(node.Steps, data, resultMap, cmdTmpl, request, cwdTmpl, stdinTmpl, captureExec, execStderr)
	if err != nil {
		return err
	}
	executions := oc.executions
	if oc.code != 0 {
		exitCode = oc.code
		// The steps failed, so nothing reaches the queue. Take the display down
		// first: what follows belongs on the terminal, not in a log region that
		// has stopped updating.
		session.close()
		reportExecutions(c, executions)
		return nil
	}

	entry, err := renderEntry(node.Entry, data)
	if err != nil {
		return fmt.Errorf("render entry: %w", err)
	}
	if entry == nil {
		entry = map[string]any{}
	}
	data["entry"] = entry
	logDebug("leaf %q: entry: %s", node.Name, jsonCompact(entry))

	// The hand-off is the leaf's action: a <download> leaf never runs a command
	// of its own, so an ancestor's <run> stays where it is instead of firing an
	// unrelated request on the way to the queue.
	if session != nil {
		logVerbose("leaf %q: handing %d download declaration(s) to the queue", node.Name, len(node.Downloads))
		exitCode = session.run(node.Downloads, data)
		reportExecutions(c, executions+1)
		return nil
	}

	if !cmdTmpl.Defined() && !request.Defined() {
		return fmt.Errorf("no command or request available to run")
	}

	leafCwd, err := renderCwd(cwdTmpl, data)
	if err != nil {
		return fmt.Errorf("render cwd: %w", err)
	}

	leafStdin, err := renderStdin(stdinTmpl, data)
	if err != nil {
		return fmt.Errorf("render stdin: %w", err)
	}

	logVerbose("leaf %q: executing", node.Name)
	exitCode, err = execLeaf(c, cmdTmpl, request, leafCwd, leafStdin, data, node.Fields, formatRef, formats)
	if err != nil {
		return err
	}
	logVerbose("leaf %q: exit code %d", node.Name, exitCode)
	executions++
	logVerbose("leaf %q: %d executions total", node.Name, executions)
	reportExecutions(c, executions)
	return nil
}

// renderCwd renders a cwd template against data. An empty template short-
// circuits to the empty string ("no override"), so we don't pay template
// machinery on the common no-cwd path.
func renderCwd(tmpl string, data any) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	return renderString(tmpl, data)
}

// renderStdin renders a stdin template against data. An empty template short-
// circuits to the empty string ("no override" — the child inherits the parent
// process's stdin).
func renderStdin(tmpl string, data any) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	return renderString(tmpl, data)
}

// reportExecutions prints the number of commands run to stderr when n > 1
// and --quiet is not set.
func reportExecutions(c *cobra.Command, n int) {
	if n <= 1 {
		return
	}
	quiet, _ := c.Root().PersistentFlags().GetBool("quiet")
	if !quiet {
		fmt.Fprintf(execStderr, "%d executions\n", n)
	}
}

// resolveContext assembles a leaf's data context from its non-flag half (base:
// .arg, .env, and .rest in passthrough mode), the flags this invocation
// supplies, and the merged vars.
//
// Vars resolve twice, because the two halves depend on each other: a templated
// flag default reads .var, and a var reads .flag. Pass one runs without flags
// purely to feed those defaults. Pass two runs again over the original
// templates once gather has produced the whole flag map, cobra's declared
// defaults included. Everything downstream — a URL, an entry, a jq program
// kept in a <var> — therefore sees this run's flags.
func resolveContext(vars, base map[string]any, gather func(preFlag map[string]any) (map[string]any, error)) (map[string]any, error) {
	preFlag := maps.Clone(base)
	preFlag["flag"] = map[string]any{}
	firstPass, err := renderVars(vars, preFlag)
	if err != nil {
		return nil, fmt.Errorf("render vars: %w", err)
	}
	preFlag["var"] = firstPass

	flagMap, err := gather(preFlag)
	if err != nil {
		return nil, err
	}

	data := maps.Clone(base)
	data["flag"] = flagMap
	renderedVars, err := renderVars(vars, data)
	if err != nil {
		return nil, fmt.Errorf("render vars: %w", err)
	}
	data["var"] = renderedVars
	return data, nil
}

// maxVarPasses caps the fixpoint iteration that resolves inter-var references.
const maxVarPasses = 10

// renderVars runs each string leaf of the merged vars map through the template
// engine with the given context. Vars may reference other vars: each pass makes
// the previous pass's resolved values available at `.var`, iterating to a
// fixpoint (capped at maxVarPasses). The original templates are re-rendered each
// pass, so already-resolved text is never double-processed. Non-string values
// pass through.
func renderVars(vars map[string]any, data map[string]any) (map[string]any, error) {
	if len(vars) == 0 {
		return map[string]any{}, nil
	}
	// Round-trip via JSON so we can reuse the entry walker. This preserves
	// the structure exactly and handles nested maps/slices of strings.
	raw, err := json.Marshal(vars)
	if err != nil {
		return nil, err
	}
	cur := map[string]any{}
	for pass := 0; pass < maxVarPasses; pass++ {
		ctx := make(map[string]any, len(data)+1)
		for k, v := range data {
			ctx[k] = v
		}
		ctx["var"] = cur
		v, err := renderEntry(raw, ctx)
		if err != nil {
			return nil, err
		}
		next, ok := v.(map[string]any)
		if !ok {
			if v == nil {
				return map[string]any{}, nil
			}
			return nil, fmt.Errorf("vars did not render to a map: got %T", v)
		}
		if pass > 0 && varsEqual(cur, next) {
			return next, nil
		}
		cur = next
	}
	return cur, nil
}

// varsEqual reports whether two rendered var maps are equal by JSON identity.
func varsEqual(a, b map[string]any) bool {
	ba, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ba, bb)
}
