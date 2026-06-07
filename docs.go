package main

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed README.md
var readmeDoc string

//go:embed api.schema.xsd
var schemaDoc string

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
