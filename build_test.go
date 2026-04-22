package main

import (
	"encoding/json"
	"testing"
)

const miniConfig = `{
  "name": "t",
  "defaults": {"base_url": "https://x.example"},
  "commands": [
    {
      "name": "users",
      "short": "u short",
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
	if err := json.Unmarshal([]byte(miniConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := validate(&cfg); err != nil {
		t.Fatal(err)
	}
	root := newRoot(&cfg)

	users, _, err := root.Find([]string{"users"})
	if err != nil || users == nil {
		t.Fatal("users subcommand missing:", err)
	}
	if users.Short != "u short" {
		t.Errorf("users.Short = %q", users.Short)
	}

	get, _, err := root.Find([]string{"users", "get"})
	if err != nil {
		t.Fatal(err)
	}
	if get.Use != "get <id>" {
		t.Errorf("get.Use = %q, want %q", get.Use, "get <id>")
	}

	list, _, err := root.Find([]string{"users", "list"})
	if err != nil {
		t.Fatal(err)
	}
	limit := list.Flags().Lookup("limit")
	if limit == nil {
		t.Fatal("--limit flag missing")
	}
	if limit.Shorthand != "l" {
		t.Errorf("limit short = %q, want %q", limit.Shorthand, "l")
	}
	if limit.DefValue != "10" {
		t.Errorf("limit default = %q, want %q", limit.DefValue, "10")
	}
	if tok := list.Flags().Lookup("token"); tok == nil {
		t.Error("--token flag missing")
	}
}

func TestValidate_RejectsReservedName(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: "https://x.example"},
		Commands: []Command{{Name: "help", Request: &Request{Method: "GET", Path: "/"}}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for reserved name")
	}
}

func TestValidate_RejectsArgFlagCollision(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: "https://x.example"},
		Commands: []Command{{
			Name:    "x",
			Args:    []Arg{{Name: "id", Required: true}},
			Flags:   []Flag{{Name: "id"}},
			Request: &Request{Method: "GET", Path: "/"},
		}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for arg/flag name collision")
	}
}

func TestValidate_RejectsLeafWithoutMethod(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: "https://x.example"},
		Commands: []Command{{Name: "x", Request: &Request{Path: "/"}}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for request.method missing")
	}
}

func TestValidate_RejectsGroupWithNoChildren(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: "https://x.example"},
		Commands: []Command{{Name: "x"}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected error for node with neither request nor subcommands")
	}
}

func TestFindConfigFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--config", "path.json", "foo"}, "path.json"},
		{[]string{"--config=path.json", "foo"}, "path.json"},
		{[]string{"foo", "--config", "path.json"}, "path.json"},
		{[]string{"foo"}, ""},
		{[]string{"--config"}, ""}, // dangling flag: no value
	}
	for _, c := range cases {
		got := findConfigFlag(c.args)
		if got != c.want {
			t.Errorf("findConfigFlag(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}
