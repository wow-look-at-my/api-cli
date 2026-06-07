package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests guard the shipped GitHub sample config (samples/github/github.json)
// against a class of bug that is invisible to schema validation: a leaf command
// silently inheriting a format whose views were written for a *different* JSON
// shape.
//
// The original failure: `repo contents` declared no format of its own, so it
// inherited `format: "repo"` from the `repo` command group. GitHub's contents
// API returns file/dir entries ({name,path,sha,size,type}), which carry none of
// the repo fields the repo views read (full_name/stargazers_count/...). A
// directory listing (JSON array) hit the repo `table` view and rendered
// `NAME STARS FORKS LANG DESCRIPTION` with every cell as the missingkey=zero
// sentinel (`<nil>`); a single file (JSON object) hit the repo `detail` view and
// rendered `Repo:/Stars:/...` as `<no value>`. View selection only switches on
// array-vs-object, never the schema, so nothing caught it.
//
// We exercise the real seam: collectMCPLeaves resolves format inheritance
// exactly as buildMCPServer does, and mcpFormat is the precise function that
// rendered the garbage in production.

const githubSampleConfig = "samples/github/github.json"

// githubLeaf loads the shipped GitHub sample and returns the resolved leaf with
// the given (underscore-joined) MCP tool name, alongside the config it came
// from so callers can compare resolved formats within a single Load.
func githubLeaf(t *testing.T, name string) (*Config, *mcpLeaf) {
	t.Helper()
	cfg, err := Load(githubSampleConfig)
	require.NoError(t, err, "the shipped GitHub sample config must load and validate")
	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{
		vars:    cfg.Vars,
		cmd:     cfg.Command,
		cwd:     cfg.Cwd,
		stdin:   cfg.Stdin,
		formats: cfg.Formats,
	})
	for i := range leaves {
		if leaves[i].name == name {
			return cfg, &leaves[i]
		}
	}
	t.Fatalf("leaf %q not found in %s", name, githubSampleConfig)
	return nil, nil
}

// contentsData is a minimal-but-realistic data context for rendering the
// contents views. The views only read .data, but mcpFormat threads the full
// context through, so we populate the usual namespaces.
func contentsData() map[string]any {
	return map[string]any{
		"arg":  map[string]any{"repo": "owner/name", "path": "."},
		"flag": map[string]any{"ref": ""},
		"env":  map[string]any{},
		"var":  map[string]any{},
	}
}

// assertNoMissingKeySentinels fails if the rendered output contains the
// text/template missingkey=zero markers, which appear whenever a view reads a
// field the payload doesn't have — the exact symptom of the contents bug.
func assertNoMissingKeySentinels(t *testing.T, out string) {
	t.Helper()
	for _, sentinel := range []string{"<no value>", "<nil>"} {
		assert.NotContainsf(t, out, sentinel,
			"rendered output contains missingkey sentinel %q — a view is reading fields the payload does not have:\n%s",
			sentinel, out)
	}
}

func TestGithubSample_Loads(t *testing.T) {
	cfg, leaf := githubLeaf(t, "repo_contents")
	require.NotNil(t, cfg)
	require.NotNil(t, leaf)
	// A couple of sibling leaves should also resolve, confirming the tree wired up.
	for _, name := range []string{"repo_get", "repo_issues", "user_get", "search_repos"} {
		_, l := githubLeaf(t, name)
		assert.NotNil(t, l, "expected leaf %q in the GitHub sample", name)
	}
}

func TestGithubSample_RepoContents_DirectoryListing(t *testing.T) {
	_, leaf := githubLeaf(t, "repo_contents")

	// What GitHub returns for a directory: a JSON array of entries (post jq
	// url-stripping leaves name/path/sha/size/type).
	dir := `[` +
		`{"name":"README.md","path":"README.md","sha":"aaa111","size":4096,"type":"file"},` +
		`{"name":"llama","path":"llama","sha":"bbb222","size":0,"type":"dir"}` +
		`]`

	out, ok := mcpFormat(leaf, dir, contentsData())
	require.True(t, ok, "format should apply to a directory listing")

	// The actual entries must be rendered.
	assert.Contains(t, out, "README.md")
	assert.Contains(t, out, "llama")
	assert.Contains(t, out, "file")
	assert.Contains(t, out, "dir")

	// And none of the repo-table garbage from the inherited-format bug.
	assertNoMissingKeySentinels(t, out)
	assert.NotContains(t, out, "STARS", "repo table header leaked into contents output")
	assert.NotContains(t, out, "FORKS", "repo table header leaked into contents output")
}

func TestGithubSample_RepoContents_SingleFile(t *testing.T) {
	_, leaf := githubLeaf(t, "repo_contents")

	// What GitHub returns for a file: a JSON object.
	file := `{"name":"Dockerfile","path":"Dockerfile","sha":"deadbeefcafe","size":1328,"type":"file","encoding":"base64"}`

	out, ok := mcpFormat(leaf, file, contentsData())
	require.True(t, ok, "format should apply to a single file object")

	assert.Contains(t, out, "Dockerfile")
	assert.Contains(t, out, "1328")

	// The repo `detail` view would have produced "Repo:"/"Stars:" with empties.
	assertNoMissingKeySentinels(t, out)
	assert.NotContains(t, out, "Repo:", "repo detail layout leaked into contents output")
	assert.NotContains(t, out, "Stars:", "repo detail layout leaked into contents output")
}

// TestGithubSample_RepoContents_HasOwnFormat encodes the root cause directly:
// `repo contents` must resolve to a format that is NOT the shared `repo` format
// it would otherwise inherit from the `repo` command group.
func TestGithubSample_RepoContents_HasOwnFormat(t *testing.T) {
	cfg, leaf := githubLeaf(t, "repo_contents")

	resolved := resolveFormat(leaf.formatRef, leaf.formats)
	require.NotNil(t, resolved, "repo contents must resolve to a format")

	// Same Load, so pointer identity is meaningful: inheriting `repo` would make
	// these the same *Format.
	assert.NotSame(t, cfg.Formats["repo"], resolved,
		"repo contents must declare its own format, not inherit the repo list/detail views")
}
