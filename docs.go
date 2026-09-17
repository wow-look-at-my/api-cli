package main

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
	spec "github.com/wow-look-at-my/api-cli-spec"
)

//go:embed README.md
var readmeDoc string

// The grammar comes from the specification module, so this binary prints the
// one text rather than a copy of it.
var schemaDoc = spec.Schema

//go:embed api.example.xml
var exampleDoc string

func docsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "docs",
		Short:        "Print embedded documentation",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(execStdout, readmeDoc)
			return nil
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:          "schema",
		Short:        "Print the XSD schema for config files",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(execStdout, schemaDoc)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:          "example",
		Short:        "Print the example config (api.example.xml)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(execStdout, exampleDoc)
			return nil
		},
	})

	return cmd
}

func isDocsInvocation(argv []string) bool {
	for _, a := range argv {
		if a == "docs" {
			return true
		}
	}
	return false
}
