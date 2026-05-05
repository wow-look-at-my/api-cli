package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is the process body, split out of main for testability. argv is the
// slice of arguments (os.Args[1:] in production); errOut receives diagnostics.
func run(argv []string, errOut io.Writer) int {
	cfgPath := findConfigFlag(argv)
	if cfgPath == "" {
		if _, err := os.Stat("api.json"); err == nil {
			cfgPath = "api.json"
		}
	}

	mcpTransport := findMcpFlag(argv)

	var cfg *Config
	if cfgPath != "" {
		loaded, err := Load(cfgPath)
		if err != nil {
			fmt.Fprintln(errOut, "error:", err)
			return 2
		}
		cfg = loaded
	}

	// No config and user invoked a real subcommand — they need a config.
	// Bare invocation (no args) and help flags fall through to cobra so the
	// user sees --help output. --mcp mode always requires a config, so don't
	// exempt help invocations when --mcp is present (that would panic).
	if cfg == nil && ((!isHelpInvocation(argv) && !isDocsInvocation(argv)) || mcpTransport != "") {
		fmt.Fprintln(errOut, "error: no config found; pass --config <path> or place api.json in the current directory")
		return 2
	}

	if mcpTransport != "" {
		return runMCP(mcpTransport, cfg)
	}

	root := newRoot(cfg)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		return 1
	}
	return exitCode
}

// newRoot builds the root cobra.Command. When cfg is nil, the root has no
// subcommands — it exists solely so `--help` and bare invocation can print
// cobra's usage screen with the --config flag visible.
func newRoot(cfg *Config) *cobra.Command {
	name := "api-cli"
	short := "Declarative CLI. Provide a config via --config <path> or ./api.json."
	if cfg != nil {
		name = cfg.Name
		if cfg.Description != "" {
			short = cfg.Description
		}
	}

	root := &cobra.Command{
		Use:          name,
		Short:        short,
		SilenceUsage: true,
	}
	// Declared so --help lists them. Actual parsing of --config and --mcp
	// happens before the cobra tree is built (findConfigFlag / findMcpFlag).
	root.PersistentFlags().String("config", "", "Path to JSON config file (default: ./api.json).")
	root.PersistentFlags().String("mcp", "", `Run as MCP server. Value: "stdio", "http://<addr>", or "sse://<addr>".`)
	root.PersistentFlags().BoolP("quiet", "q", false, "Suppress execution count on stderr.")
	root.PersistentFlags().BoolP("yes", "y", false, "Skip confirmation prompts.")
	root.PersistentFlags().Bool("no-format", false, "Disable output formatting (synonym for --format=raw).")
	root.PersistentFlags().String("format", "auto", "Output formatting mode: raw|auto|always.")
	root.PersistentFlags().String("view", "", "Select a named view from the active format (overrides selectors).")

	if cfg != nil {
		for _, c := range cfg.Commands {
			root.AddCommand(buildCommand(c, cfg.Vars, cfg.Command, cfg.Cwd, cfg.Stdin, "", nil, cfg.Formats))
		}
	} else {
		// Cobra's default help template only renders the flags/usage block
		// for Runnable commands (or commands with subcommands). Without a
		// config we have neither, so give the root a RunE that prints help —
		// otherwise bare invocation and `--help` would emit just the Short.
		root.RunE = func(c *cobra.Command, args []string) error {
			return c.Help()
		}
	}
	root.AddCommand(docsCommand())
	return root
}

// isHelpInvocation reports whether argv is a plain help request: no args at
// all, or any form of the help flag/subcommand. Used to decide whether a
// missing config is fatal or should fall through to cobra's usage screen.
func isHelpInvocation(argv []string) bool {
	if len(argv) == 0 {
		return true
	}
	for _, a := range argv {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	return false
}

// findConfigFlag walks the argv looking for --config=<value> or --config <value>.
// Returns the empty string if no value is found.
func findConfigFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config=")
		}
		if a == "--config" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
