package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const miniConfig = `{
  "name": "t",
  "command": "true",
  "commands": [
    {
      "name": "users",
      "description": "u desc",
      "commands": [
        {
          "name": "get",
          "args": [{"name": "id", "type": "int", "required": true}],
          "entry": {"path": "/users/{{.arg.id}}"}
        },
        {
          "name": "list",
          "flags": [
            {"name": "limit", "short": "l", "type": "int", "default": 10},
            {"name": "token", "type": "string", "required": true}
          ],
          "entry": {"path": "/users"}
        }
      ]
    }
  ]
}`

func TestBuildTreeShape(t *testing.T) {
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(miniConfig), &cfg))
	require.NoError(t, validate(&cfg))

	root := newRoot(&cfg)

	users, _, err := root.Find([]string{"users"})
	require.NoError(t, err)
	require.NotNil(t, users)
	assert.Equal(t, "u desc", users.Short)

	get, _, err := root.Find([]string{"users", "get"})
	require.NoError(t, err)
	assert.Equal(t, "get <id>", get.Use)

	list, _, err := root.Find([]string{"users", "list"})
	require.NoError(t, err)

	limit := list.Flags().Lookup("limit")
	require.NotNil(t, limit)
	assert.Equal(t, "l", limit.Shorthand)
	assert.Equal(t, "10", limit.DefValue)
	assert.NotNil(t, list.Flags().Lookup("token"))
}

func TestCmd_UnmarshalBothForms(t *testing.T) {
	// String form.
	var c1 Cmd
	require.NoError(t, json.Unmarshal([]byte(`"echo {{.x}}"`), &c1))
	assert.True(t, c1.Shell)
	assert.Equal(t, "echo {{.x}}", c1.Template)

	// Array form.
	var c2 Cmd
	require.NoError(t, json.Unmarshal([]byte(`["echo","{{.x}}"]`), &c2))
	assert.False(t, c2.Shell)
	assert.Equal(t, []string{"echo", "{{.x}}"}, c2.Argv)

	// Null → not defined.
	var c3 Cmd
	require.NoError(t, json.Unmarshal([]byte(`null`), &c3))
	assert.False(t, c3.Defined())

	// Bad type.
	var c4 Cmd
	err := json.Unmarshal([]byte(`42`), &c4)
	assert.Error(t, err)
}

func TestValidate_RejectsReservedName(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Command:  &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{Name: "help"}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_RejectsEntryOnGroup(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name:     "x",
			Entry:    json.RawMessage(`{"path": "/"}`),
			Commands: []Command{{Name: "y"}},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_LeafWithoutCommandFailsWithoutAncestor(t *testing.T) {
	// No root command, no leaf command → fail.
	cfg := &Config{
		Name:     "t",
		Commands: []Command{{Name: "x"}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_LeafInheritsAncestorCommand(t *testing.T) {
	// Root has command, leaf doesn't → passes.
	cfg := &Config{
		Name:     "t",
		Command:  &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{Name: "x"}},
	}
	err := validate(cfg)
	assert.NoError(t, err)
}

func TestValidate_DuplicateSiblingNames(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{
			{Name: "x"},
			{Name: "x"},
		},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_DuplicateArgName(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Args: []Arg{
				{Name: "a", Required: true},
				{Name: "a"},
			},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_DuplicateFlagShort(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Flags: []Flag{
				{Name: "a", Short: "x"},
				{Name: "b", Short: "x"},
			},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_VariadicMustBeLast(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Args: []Arg{
				{Name: "rest", Variadic: true, Required: true},
				{Name: "extra", Required: true},
			},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_ConflictsMustReferenceRealFlag(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Flags: []Flag{
				{Name: "a", Conflicts: []string{"ghost"}},
			},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_ConflictsCannotReferenceSelf(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Flags: []Flag{
				{Name: "a", Conflicts: []string{"a"}},
			},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_FlagNameNoNoPrefix(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Flags: []Flag{
				{Name: "no-cache", Type: "bool"},
			},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_PreconditionsLeafOnly(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name:          "x",
			Preconditions: []string{"oops"},
			Commands:      []Command{{Name: "y"}},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}

func TestValidate_ConfirmAllowedOnGroup(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name:     "x",
			Confirm:  "are you sure?",
			Commands: []Command{{Name: "y"}},
		}},
	}
	err := validate(cfg)
	assert.NoError(t, err)
}

func TestVariadicArgUsesString(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Args: []Arg{
				{Name: "files", Variadic: true},
			},
		}},
	}
	require.NoError(t, validate(cfg))
	root := newRoot(cfg)
	cmd, _, err := root.Find([]string{"x"})
	require.NoError(t, err)
	// useStr ends with [files...]
	assert.Contains(t, cmd.Use, "files...")
}
