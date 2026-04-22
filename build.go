package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

// exitCode is set by a leaf's RunE to reflect the HTTP response (0/4/5/1).
// main reads it after rootCmd.Execute() returns.
var exitCode int

// buildCommand turns a single Command node into a cobra.Command, wiring up
// args, flags, subcommands, and (for leaves) an HTTP-issuing RunE.
func buildCommand(node Command, defaults Defaults) *cobra.Command {
	useStr := node.Name
	requiredArgs := 0
	for _, a := range node.Args {
		if a.Required {
			useStr += " <" + a.Name + ">"
			requiredArgs++
		} else {
			useStr += " [" + a.Name + "]"
		}
	}

	cmd := &cobra.Command{
		Use:          useStr,
		Short:        node.Short,
		Long:         node.Long,
		SilenceUsage: true,
	}

	if total := len(node.Args); total > 0 {
		if requiredArgs == total {
			cmd.Args = cobra.ExactArgs(total)
		} else {
			cmd.Args = cobra.RangeArgs(requiredArgs, total)
		}
	}

	for _, f := range node.Flags {
		registerFlag(cmd, f)
	}

	if node.Request != nil {
		req := *node.Request
		nodeCopy := node
		defs := defaults
		cmd.RunE = func(c *cobra.Command, args []string) error {
			data, err := gatherData(c, nodeCopy, args)
			if err != nil {
				return err
			}
			return runLeaf(req, defs, data)
		}
	}

	for _, child := range node.Commands {
		cmd.AddCommand(buildCommand(child, defaults))
	}

	return cmd
}

func registerFlag(cmd *cobra.Command, f Flag) {
	typ := f.Type
	if typ == "" {
		typ = "string"
	}
	switch typ {
	case "string":
		def, _ := f.Default.(string)
		cmd.Flags().StringP(f.Name, f.Short, def, f.Description)
	case "bool":
		def, _ := f.Default.(bool)
		cmd.Flags().BoolP(f.Name, f.Short, def, f.Description)
	case "int":
		var def int
		switch v := f.Default.(type) {
		case float64:
			def = int(v) // JSON numbers decode as float64 into any
		case int:
			def = v
		}
		cmd.Flags().IntP(f.Name, f.Short, def, f.Description)
	case "string-slice":
		var def []string
		if raw, ok := f.Default.([]any); ok {
			for _, x := range raw {
				if s, ok := x.(string); ok {
					def = append(def, s)
				}
			}
		}
		// StringArray (not StringSlice) so commas in values are preserved.
		cmd.Flags().StringArrayP(f.Name, f.Short, def, f.Description)
	}
	if f.Required {
		_ = cmd.MarkFlagRequired(f.Name)
	}
}

// gatherData assembles the template context: positional args by name, flag
// values by name, and the process environment under .env.
func gatherData(cmd *cobra.Command, node Command, args []string) (map[string]any, error) {
	data := map[string]any{"env": envMap()}
	for i, a := range node.Args {
		if i >= len(args) {
			break
		}
		v := args[i]
		if a.Type == "int" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("arg %q: %w", a.Name, err)
			}
			data[a.Name] = n
			continue
		}
		data[a.Name] = v
	}
	for _, f := range node.Flags {
		typ := f.Type
		if typ == "" {
			typ = "string"
		}
		switch typ {
		case "string":
			v, _ := cmd.Flags().GetString(f.Name)
			data[f.Name] = v
		case "bool":
			v, _ := cmd.Flags().GetBool(f.Name)
			data[f.Name] = v
		case "int":
			v, _ := cmd.Flags().GetInt(f.Name)
			data[f.Name] = v
		case "string-slice":
			v, _ := cmd.Flags().GetStringArray(f.Name)
			data[f.Name] = v
		}
	}
	return data, nil
}

// runLeaf renders the request templates and executes the HTTP call.
func runLeaf(req Request, defs Defaults, data map[string]any) error {
	path, err := renderString(req.Path, data)
	if err != nil {
		return fmt.Errorf("render path: %w", err)
	}
	query, err := renderMap(req.Query, data)
	if err != nil {
		return fmt.Errorf("render query: %w", err)
	}
	headers := map[string]string{}
	defHeaders, err := renderMap(defs.Headers, data)
	if err != nil {
		return fmt.Errorf("render default headers: %w", err)
	}
	for k, v := range defHeaders {
		headers[k] = v
	}
	reqHeaders, err := renderMap(req.Headers, data)
	if err != nil {
		return fmt.Errorf("render request headers: %w", err)
	}
	for k, v := range reqHeaders {
		headers[k] = v
	}
	body, err := renderBody(req.Body, data)
	if err != nil {
		return fmt.Errorf("render body: %w", err)
	}
	if len(body) > 0 {
		if _, ok := headers["Content-Type"]; !ok {
			headers["Content-Type"] = "application/json"
		}
	}

	exitCode = doRequest(req.Method, defs.BaseURL, path, query, headers, body)
	return nil
}
