package main

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	spec "github.com/wow-look-at-my/api-cli-spec"
)

//go:embed README.md
var readmeText string

//go:embed docs/*.md
var docPages embed.FS

// readmeDoc is the README followed by every page under docs/.
var readmeDoc = joinDocs(readmeText, docPages)

func joinDocs(readme string, pages fs.FS) string {
	names, err := fs.Glob(pages, "docs/*.md")
	if err != nil {
		panic(err)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(readme)
	for _, name := range names {
		body, err := fs.ReadFile(pages, name)
		if err != nil {
			panic(err)
		}
		b.WriteString("\n")
		b.Write(body)
	}
	return b.String()
}

// The grammar comes from the specification module.
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
