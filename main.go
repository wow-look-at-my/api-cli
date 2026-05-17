package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is the process body, split out of main for testability. argv is the
// slice of arguments (os.Args[1:] in production); errOut receives diagnostics.
func run(argv []string, errOut io.Writer) int {
	cfgPath, mcpTransport, corsValue := preparseGlobalFlags(argv)
	if cfgPath == "" {
		if _, err := os.Stat("api.json"); err == nil {
			cfgPath = "api.json"
		}
	}

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
		corsLevel, err := parseCorsLevel(corsValue)
		if err != nil {
			fmt.Fprintln(errOut, "error:", err)
			return 2
		}
		return runMCP(mcpTransport, cfg, corsLevel)
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
	// Declared so --help lists them. In MCP mode we extract --config /
	// --mcp / --cors from argv before the cobra tree exists; see
	// preparseGlobalFlags.
	root.PersistentFlags().String("config", "", "Path to JSON config file (default: ./api.json).")
	root.PersistentFlags().String("mcp", "", `Run as MCP server. Value: "stdio", "http://<addr>", or "sse://<addr>".`)
	root.PersistentFlags().String("cors", "strict", "CORS policy for MCP HTTP/SSE: disabled|permissive|strict|enabled.")
	root.PersistentFlags().BoolP("quiet", "q", false, "Suppress execution count on stderr.")
	root.PersistentFlags().BoolP("yes", "y", false, "Skip confirmation prompts.")
	root.PersistentFlags().Bool("verbose", false, "Show commands being executed, exit codes, and condition results on stderr.")
	root.PersistentFlags().Bool("debug", false, "Show full execution details on stderr (implies --verbose).")
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

// preparseGlobalFlags pulls --config, --mcp, and --cors out of argv with
// a throwaway cobra.Command. We need these values before the real root
// tree exists, because --mcp decides whether we even build that tree.
// FParseErrWhitelist.UnknownFlags lets the parse skip over subcommand
// flags it doesn't know about; positional args (subcommand names) fall
// through harmlessly.
//
// The defaults registered here mirror those on the real root in newRoot.
func preparseGlobalFlags(argv []string) (configPath, mcpTransport, corsValue string) {
	pre := &cobra.Command{SilenceErrors: true, SilenceUsage: true}
	pre.SetOut(io.Discard)
	pre.SetErr(io.Discard)
	pre.FParseErrWhitelist.UnknownFlags = true

	pre.Flags().String("config", "", "")
	pre.Flags().String("mcp", "", "")
	pre.Flags().String("cors", "strict", "")

	_ = pre.ParseFlags(argv)
	configPath, _ = pre.Flags().GetString("config")
	mcpTransport, _ = pre.Flags().GetString("mcp")
	corsValue, _ = pre.Flags().GetString("cors")
	return
}
