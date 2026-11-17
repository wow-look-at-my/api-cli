package main

import _ "embed"

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// partServer answers /part/<group>/<n> with a body that names the part, so the
// order of the parts is visible in the output.
func partServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "[%s]", strings.TrimPrefix(r.URL.Path, "/part/"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// joinRunConfig is a two-group join whose parts land in partsDir, with run as
// the <join>'s program.
func joinRunConfig(srv *httptest.Server, partsDir, run string) string {
	return strings.NewReplacer("@URL@", srv.URL, "@PARTS@", partsDir, "@RUN@", run).Replace(joinRunConfigXml)
}

func TestJoinRun_Parses(t *testing.T) {
	cfg, err := loadStr(t, testJoinRun_ParsesXml)
	require.NoError(t, err)
	run := cfg.Commands[0].Downloads[0].Join.Run
	require.NotNil(t, run)
	assert.Equal(t, []string{"ffmpeg", "-i", "{{ .join.input }}", "{{ .join.output }}"}, run.Argv)
}

func TestJoinRun_RejectsBadGrammar(t *testing.T) {
	cases := []struct {
		name string
		join string
		want string
	}{
		{"request", `<join to="o"><run><request><url>https://x.test</url></request></run></join>`, "not a <request>"},
		{"other child", `<join to="o"><step/></join>`, "unexpected child element <step>"},
		{"twice", `<join to="o"><run>true</run><run>true</run></join>`, "declares <run> twice"},
		{"empty", `<join to="o"><run>  </run></join>`, "the program is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadStr(t, strings.Replace(testJoinRun_RejectsBadGrammarXml, "@JOIN@", tc.join, 1))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// .join.input sits in the scratch directory. This happens when the run has one,
// and next to the output when it does not. .join.output is always the output's
// .part sibling.
func TestJoinRun_PlansInputAndOutput(t *testing.T) {
	d := Download{
		URL:   "https://example.test/x",
		To:    "p.part",
		Group: "g",
		Join:  &Join{To: "clips/{{ .id }}.mp4", Run: &Cmd{Argv: []string{"mux", "{{ .join.input }}", "{{ .join.output }}", "{{ .fmt }}"}}},
	}

	specs, err := planDownloads([]Download{d}, map[string]any{"id": "a", "fmt": "h264", "run": map[string]any{"tmpdir": "/scratch"}}, "/out")
	require.NoError(t, err)
	part := specs[0].Join
	assert.Equal(t, "/scratch/join/clips/a.mp4.joined", part.Input)
	assert.Equal(t, []string{"mux", "/scratch/join/clips/a.mp4.joined", "/out/clips/a.mp4.part", "h264"}, part.Argv)

	specs, err = planDownloads([]Download{d}, map[string]any{"id": "a", "fmt": "h264"}, "/out")
	require.NoError(t, err)
	assert.Equal(t, "/out/clips/a.mp4.joined", specs[0].Join.Input)
}

// The program reads the joined parts and writes the output. While it runs, the
// output exists only under its .part name.
func TestIntegration_JoinRunTransformsTheOutput(t *testing.T) {
	srv := partServer(t)
	swapDownloadClient(t, srv)
	out, parts := t.TempDir(), t.TempDir()

	run := `<run>f=<value name="join.output"/>; case $f in *.part) ;; *) exit 9;; esac; test ! -e "${f%.part}" || exit 8; tr a-z A-Z &lt; <value name="join.input"/> &gt; "$f"</run>`
	cfg, err := loadStr(t, joinRunConfig(srv, parts, run))
	require.NoError(t, err)

	code, stdout, errOut := execCmdFull(t, cfg, "pull", "--download-dir", out)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	for _, id := range []string{"good", "bad"} {
		body, rerr := os.ReadFile(filepath.Join(out, "clips", id+".out"))
		require.NoError(t, rerr, "the program writes into a directory the join made for it")
		want := strings.ToUpper(fmt.Sprintf("[%s/1][%s/2]", id, id))
		assert.Equal(t, want, string(body))
		assert.NoFileExists(t, filepath.Join(out, "clips", id+".out.part"))
		assert.NoDirExists(t, filepath.Join(parts, id), "cleanup= removes the parts")
	}
	assert.Contains(t, errOut, "running sh for good.out")
	assert.Contains(t, errOut, "joined good.out (2 parts)")
	assert.ElementsMatch(t, []string{filepath.Join(out, "clips", "good.out"), filepath.Join(out, "clips", "bad.out")}, strings.Fields(stdout),
		"stdout lists each group's output once, never its parts or the joined input")
}

// A program that fails fails its group only. The output never gets its real
// name, the parts stay, and the run exits non-zero.
func TestIntegration_JoinRunFailureKeepsTheParts(t *testing.T) {
	srv := partServer(t)
	swapDownloadClient(t, srv)
	out, parts := t.TempDir(), t.TempDir()

	run := `<run>echo "muxer says hi" &gt;&amp;2; printf partial &gt; <value name="join.output"/>; test <value name="id"/> != bad || exit 3; cp <value name="join.input"/> <value name="join.output"/></run>`
	cfg, err := loadStr(t, joinRunConfig(srv, parts, run))
	require.NoError(t, err)

	code, stdout, errOut := execCmdFull(t, cfg, "pull", "--download-dir", out)
	assert.Equal(t, 1, code)
	assert.Equal(t, filepath.Join(out, "clips", "good.out")+"\n", stdout, "a group that failed lists nothing")
	assert.Contains(t, errOut, `group "bad": <join><run>`)
	assert.Contains(t, errOut, "exited with exit status 3")
	assert.Contains(t, errOut, "the parts are kept")
	assert.Contains(t, errOut, "muxer says hi", "the program's stderr reaches the user")

	assert.NoFileExists(t, filepath.Join(out, "clips", "bad.out"))
	assert.NoFileExists(t, filepath.Join(out, "clips", "bad.out.part"))
	assert.FileExists(t, filepath.Join(parts, "bad", "1.part"), "a failed group keeps its parts, cleanup= or not")
	assert.FileExists(t, filepath.Join(parts, "bad", "2.part"))

	body, err := os.ReadFile(filepath.Join(out, "clips", "good.out"))
	require.NoError(t, err, "the other group still lands")
	assert.Equal(t, "[good/1][good/2]", string(body))
}

// A program that exits 0 and writes nothing is a failure, not an empty output.
func TestIntegration_JoinRunThatWritesNothingFails(t *testing.T) {
	srv := partServer(t)
	swapDownloadClient(t, srv)
	out, parts := t.TempDir(), t.TempDir()

	cfg, err := loadStr(t, joinRunConfig(srv, parts, `<run><argv>true</argv></run>`))
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "pull", "--download-dir", out)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "exited 0 but wrote no file at .join.output")
	assert.NoFileExists(t, filepath.Join(out, "clips", "good.out"))
}

// Every part of a group must render the same program.
func TestIntegration_JoinRunMustAgreeAcrossTheGroup(t *testing.T) {
	srv := partServer(t)
	swapDownloadClient(t, srv)
	out, parts := t.TempDir(), t.TempDir()

	cfg, err := loadStr(t, joinRunConfig(srv, parts, `<run><argv>cp</argv><argv><value name="n"/></argv><argv><value name="join.output"/></argv></run>`))
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "pull", "--download-dir", out)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "it can read only the values every part of the group shares")
}

// cleanup= removes the parts and the joined input after the program succeeds.
// Without a scratch directory the input sits next to the output, so its removal
// is visible here.
func TestJoinRun_CleanupRemovesPartsAndInput(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "whole.out")
	input := dest + ".joined"
	var members []*downloadItem
	for i, body := range []string{"ab", "cd"} {
		p := filepath.Join(dir, "parts", fmt.Sprintf("%d.part", i))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		item := &downloadItem{spec: downloadSpec{Join: &joinPart{
			Group: "g", Dest: dest, Order: float64(i), Cleanup: true,
			Input: input, Argv: []string{"cp", input, dest + ".part"},
		}}}
		item.name.Store(p)
		item.state.Store(dlDone)
		members = append(members, item)
	}

	var specs []downloadSpec
	for _, m := range members {
		specs = append(specs, m.spec)
	}
	var log strings.Builder
	j := newJoiner(specs, func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) }, nil)
	for _, m := range members {
		j.note(m)
	}
	require.Empty(t, j.wait())

	body, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "abcd", string(body))
	assert.NoFileExists(t, input, "cleanup= removes the joined input")
	assert.NoDirExists(t, filepath.Join(dir, "parts"), "cleanup= removes the parts and their directory")
	assert.Contains(t, log.String(), "running cp for whole.out")
}

func TestShownArgv(t *testing.T) {
	assert.Equal(t, "ffmpeg -i /a/b.h264", shownArgv([]string{"ffmpeg", "-i", "/a/b.h264"}))
	assert.Empty(t, shownArgv(nil))
}

//go:embed testdata/join-run-config.xml.frag
var joinRunConfigXml string

//go:embed testdata/test-join-run-parses.xml
var testJoinRun_ParsesXml string

//go:embed testdata/test-join-run-rejects-bad-grammar.xml.frag
var testJoinRun_RejectsBadGrammarXml string
