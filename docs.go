package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

//go:embed README.md
var readmeDoc string

//go:embed api.schema.json
var schemaDoc string

//go:embed api.example.json
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
		Use:          "schema [key]",
		Short:        "Print JSON Schema for config files, optionally filtered to a single definition",
		SilenceUsage: true,
		Args:         cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprint(execStdout, schemaDoc)
				return nil
			}
			out, err := schemaLookup(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(execStdout, out)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:          "example",
		Short:        "Print the example config (api.example.json)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(execStdout, exampleDoc)
			return nil
		},
	})

	return cmd
}

func schemaLookup(key string) (string, error) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaDoc), &schema); err != nil {
		return "", fmt.Errorf("internal: failed to parse embedded schema: %w", err)
	}

	if defs, ok := schema["definitions"].(map[string]any); ok {
		if val, ok := defs[key]; ok {
			return marshalIndent(val)
		}
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		if val, ok := props[key]; ok {
			return marshalIndent(val)
		}
	}

	return "", fmt.Errorf("unknown schema key %q; available: %s", key, availableSchemaKeys(schema))
}

func marshalIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func availableSchemaKeys(schema map[string]any) string {
	seen := map[string]bool{}
	if defs, ok := schema["definitions"].(map[string]any); ok {
		for k := range defs {
			seen[k] = true
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		for k := range props {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func isDocsInvocation(argv []string) bool {
	for _, a := range argv {
		if a == "docs" {
			return true
		}
	}
	return false
}
