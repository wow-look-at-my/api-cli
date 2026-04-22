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
		} else {
			fmt.Fprintln(errOut, "error: no config found; pass --config <path> or place api.json in the current directory")
			return 2
		}
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}

	root := newRoot(cfg)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		return 1
	}
	return exitCode
}

// newRoot builds the root cobra.Command from a loaded Config.
func newRoot(cfg *Config) *cobra.Command {
	root := &cobra.Command{
		Use:          cfg.Name,
		Short:        cfg.Short,
		Long:         cfg.Long,
		SilenceUsage: true,
	}
	// Declared so --help lists it. Actual parsing happens in findConfigFlag
	// before the tree is built.
	root.PersistentFlags().String("config", "", "Path to JSON config file (default: ./api.json).")

	for _, c := range cfg.Commands {
		root.AddCommand(buildCommand(c, cfg.Defaults))
	}
	return root
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
