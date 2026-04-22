package main

import (
	"encoding/json"
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

const miniConfig = `{
  "name": "t",
  "defaults": {"base_url": "https://x.example"},
  "commands": [
    {
      "name": "users",
      "description": "u desc",
      "commands": [
        {
          "name": "get",
          "args": [{"name": "id", "type": "int", "required": true}],
          "request": {"method": "GET", "path": "/users/{{.id}}"}
        },
        {
          "name": "list",
          "flags": [
            {"name": "limit", "short": "l", "type": "int", "default": 10},
            {"name": "token", "type": "string", "required": true}
          ],
          "request": {"method": "GET", "path": "/users"}
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
	require.False(t, err != nil || users == nil)

	assert.Equal(t, "u desc", users.Short)

	get, _, err := root.Find([]string{"users", "get"})
	require.Nil(t, err)

	assert.Equal(t, "get <id>", get.Use)

	list, _, err := root.Find([]string{"users", "list"})
	require.Nil(t, err)

	limit := list.Flags().Lookup("limit")
	require.NotNil(t, limit)

	assert.Equal(t, "l", limit.Shorthand)

	assert.Equal(t, "10", limit.DefValue)

	tok := list.Flags().Lookup("token")
	assert.NotNil(t, tok)

}

func TestValidate_RejectsReservedName(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: "https://x.example"},
		Commands:	[]Command{{Name: "help", Request: &Request{Method: "GET", Path: "/"}}},
	}
	err := validate(cfg)
	require.NotNil(t, err)

}

func TestValidate_RejectsArgFlagCollision(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: "https://x.example"},
		Commands: []Command{{
			Name:		"x",
			Args:		[]Arg{{Name: "id", Required: true}},
			Flags:		[]Flag{{Name: "id"}},
			Request:	&Request{Method: "GET", Path: "/"},
		}},
	}
	err := validate(cfg)
	require.NotNil(t, err)

}

func TestValidate_RejectsLeafWithoutMethod(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: "https://x.example"},
		Commands:	[]Command{{Name: "x", Request: &Request{Path: "/"}}},
	}
	err := validate(cfg)
	require.NotNil(t, err)

}

func TestValidate_RejectsGroupWithNoChildren(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: "https://x.example"},
		Commands:	[]Command{{Name: "x"}},
	}
	err := validate(cfg)
	require.NotNil(t, err)

}

func TestFindConfigFlag(t *testing.T) {
	cases := []struct {
		args	[]string
		want	string
	}{
		{[]string{"--config", "path.json", "foo"}, "path.json"},
		{[]string{"--config=path.json", "foo"}, "path.json"},
		{[]string{"foo", "--config", "path.json"}, "path.json"},
		{[]string{"foo"}, ""},
		{[]string{"--config"}, ""},	// dangling flag: no value
	}
	for _, c := range cases {
		got := findConfigFlag(c.args)
		assert.Equal(t, c.want, got)

	}
}
