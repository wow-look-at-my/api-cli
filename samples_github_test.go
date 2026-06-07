package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression coverage for the shipped GitHub sample (samples/github/github.xml).
//
// History: an earlier JSON version of this sample let `repo contents` inherit
// the `repo` command's presentation spec, so a directory listing rendered as a
// repo table (NAME STARS FORKS ...) with empty cells and a single file rendered
// as a repo detail block — all missingkey sentinels, because contents records
// ({name,path,sha,size,type}) carry none of the repo fields.
//
// The current XML model fixes this structurally: `<fields>` projections do NOT
// inherit (only the legacy `<format>` does, and this sample defines none), and
// `contents` declares no projection on purpose — so it renders its own response
// (raw JSON over MCP; its own derived columns when a representation is forced).
// These tests pin that behaviour against the real sample so a future edit that
// slips a repo-shaped projection onto `contents` fails loudly.

const githubSampleConfig = "samples/github/github.xml"

// Canned GitHub "contents" API responses. Each carries a *url key so the test
// also proves the request+jq noise filter actually ran (it must be stripped).
const (
	ghDirListingJSON = `[
		{"name":"README.md","path":"README.md","sha":"aaa111","size":4096,"type":"file","download_url":"https://example.invalid/README.md"},
		{"name":"llama","path":"llama","sha":"bbb222","size":0,"type":"dir","download_url":null}
	]`
	ghFileJSON = `{"name":"Dockerfile","path":"Dockerfile","sha":"deadbeefcafe","size":1328,"type":"file","encoding":"base64","content":"RlJPTQo=","download_url":"https://example.invalid/Dockerfile"}`
)

// githubSampleWithServer loads the real sample with its HTTP base URL pointed at
// a stub that serves the contents API: a directory listing (array) for a
// `/contents` request, a single file (object) for any deeper path.
func githubSampleWithServer(t *testing.T) *Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/contents") {
			_, _ = w.Write([]byte(ghDirListingJSON))
			return
		}
		_, _ = w.Write([]byte(ghFileJSON))
	}))
	t.Cleanup(srv.Close)

	// Redirect the sample's base_url to the stub (its intended GHES override),
	// force the real noise filter (not GITHUB_RAW=identity), and drop any token.
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_RAW", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	swapHTTPClient(t, srv)

	cfg, err := Load(githubSampleConfig)
	require.NoError(t, err, "the shipped GitHub sample must load and validate")
	return cfg
}

// githubLeaf resolves a leaf by its underscore-joined MCP tool name, exactly as
// buildMCPServer does (so inheritance of run/format is applied identically).
func githubLeaf(t *testing.T, cfg *Config, name string) *mcpLeaf {
	t.Helper()
	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{
		vars:    cfg.Vars,
		cmd:     cfg.Command,
		request: cfg.Request,
		cwd:     cfg.Cwd,
		stdin:   cfg.Stdin,
		formats: cfg.Formats,
	})
	for i := range leaves {
		if leaves[i].name == name {
			return &leaves[i]
		}
	}
	t.Fatalf("leaf %q not found in %s", name, githubSampleConfig)
	return nil
}

// assertNoRepoProjectionLeak fails if the output shows the old bug's symptoms:
// missingkey sentinels, or the repo table's "stars"/"forks" columns appearing in
// contents output.
func assertNoRepoProjectionLeak(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{"<no value>", "<nil>", "stars", "forks"} {
		assert.NotContainsf(t, out, bad,
			"contents output leaked %q — a repo-shaped projection / missingkey sentinel:\n%s", bad, out)
	}
}

// Directory listing (array) over the MCP path: renders its own entries, the
// noise filter ran, and no repo projection leaked.
func TestGithubSample_RepoContents_DirectoryListing_MCP(t *testing.T) {
	cfg := githubSampleWithServer(t)
	leaf := githubLeaf(t, cfg, "repo_contents")

	out, isErr := mcpExecLeaf(leaf, map[string]any{"repo": "owner/name"})
	require.Falsef(t, isErr, "repo contents (root) should succeed; got: %s", out)

	assert.Contains(t, out, "README.md")
	assert.Contains(t, out, "llama")
	assert.Contains(t, out, "file")
	assert.Contains(t, out, "dir")
	// The request + jq noise filter actually ran: *url keys are gone.
	assert.NotContains(t, out, "download_url")
	assert.NotContains(t, out, "example.invalid")
	assertNoRepoProjectionLeak(t, out)
}

// Single file (object) over the MCP path: renders the file's own fields.
func TestGithubSample_RepoContents_SingleFile_MCP(t *testing.T) {
	cfg := githubSampleWithServer(t)
	leaf := githubLeaf(t, cfg, "repo_contents")

	out, isErr := mcpExecLeaf(leaf, map[string]any{"repo": "owner/name", "path": "Dockerfile"})
	require.Falsef(t, isErr, "repo contents (file) should succeed; got: %s", out)

	assert.Contains(t, out, "Dockerfile")
	assert.Contains(t, out, "1328")
	assert.NotContains(t, out, "download_url")
	assertNoRepoProjectionLeak(t, out)
}

// When a representation is forced (--as table), the directory listing renders
// with the entries' OWN derived columns (name/type/...), never the repo table's
// columns — the most direct guard against the original "repo table" garbage.
func TestGithubSample_RepoContents_AsTableUsesOwnColumns(t *testing.T) {
	cfg := githubSampleWithServer(t)

	code, out := execCmd(t, cfg, "repo", "contents", "owner/name", "--as", "table")
	require.Equalf(t, 0, code, "repo contents --as table should succeed; got: %s", out)

	assert.Contains(t, out, "name")
	assert.Contains(t, out, "type")
	assert.Contains(t, out, "README.md")
	assert.Contains(t, out, "llama")
	assertNoRepoProjectionLeak(t, out)
}
