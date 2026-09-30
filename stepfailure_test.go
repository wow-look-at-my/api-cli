package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clipService is a transport program that answers per clip id:
//   - flaky fails twice with a proxy error, then answers done
//   - stuck always answers pending
//   - sso answers an HTML login page
//   - ok answers done
//
// A /file/ URL is a download, and the program writes the file's contents.
const clipService = `url="$1"; state="$2"
id="${url##*/}"
case "$url" in */file/*) printf 'data-%s' "$id"; exit 0;; esac
case "$id" in
flaky)
	n=$(cat "$state/flaky" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$state/flaky"
	if [ "$n" -le 2 ]; then printf '<html>502 Bad Gateway</html>'; echo "nginx said 502" >&2; exit 1; fi
	printf '{"status":"done","url":"https://clips.example/file/flaky"}';;
stuck) printf '{"parts":[],"status":"pending"}';;
sso) printf '<!DOCTYPE html><html><title>Sign in to Example SSO</title></html>';;
*) printf '{"status":"done","url":"https://clips.example/file/%s"}' "$id";;
esac
`

// clipConfig is the shape of a real "pull": a polled listing per clip, then a
// download of every clip the listing finished.
func clipConfig(t *testing.T, stepAttrs string) *Config {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "clips.sh")
	require.NoError(t, os.WriteFile(script, []byte(clipService), 0o644))
	state := filepath.Join(dir, "state")
	require.NoError(t, os.Mkdir(state, 0o755))

	cfg, err := loadStr(t, `<config name="clips">
		<transports>
			<transport name="auth-fetch" default="true">
				<run><argv>sh</argv><argv>`+script+`</argv><argv><value name="request.url"/></argv><argv>`+state+`</argv></run>
			</transport>
		</transports>
		<command name="pull">
			<arg name="ids" variadic="true"/>
			<steps>
				<step name="listing" over="arg.ids" until="{{ eq .status &quot;done&quot; }}" interval="1s" attempts="12" `+stepAttrs+`>
					<run><request><url>https://clips.example/clips/<value name="item"/></url><response jq="."/></request></run>
				</step>
			</steps>
			<download over="result.listing">
				<url><value name="result.url"/></url>
				<to><value name="item.item"/>.bin</to>
			</download>
		</command>
	</config>`)
	require.NoError(t, err)
	return cfg
}

func TestStepFailure_RetriesAndSkipsLetTheRestDownload(t *testing.T) {
	noPollSleep(t)
	cfg := clipConfig(t, `retries="3" on-error="skip"`)
	out := t.TempDir()

	code, _, errOut := execCmdFull(t, cfg, "pull", "flaky", "stuck", "sso", "ok", "--download-dir", out)
	assert.Equal(t, 1, code, "a run that skipped anything is not a success")

	for _, id := range []string{"flaky", "ok"} {
		got, err := os.ReadFile(filepath.Join(out, id+".bin"))
		require.NoError(t, err, "stderr: %s", errOut)
		assert.Equal(t, "data-"+id, string(got))
	}
	for _, id := range []string{"stuck", "sso"} {
		assert.NoFileExists(t, filepath.Join(out, id+".bin"))
	}

	assert.Contains(t, errOut, `error: step "listing" [1/4] flaky: transport "auth-fetch" exited 1`, "a retried failure names the element")
	assert.Contains(t, errOut, `step "listing" [1/4] flaky: retry 2/3`)
	assert.Contains(t, errOut, `502 Bad Gateway`, "the start of the program's stdout goes with its failure")
	assert.Contains(t, errOut, "listing 1/4 flaky: done")

	assert.Contains(t, errOut, `error: step "listing" [2/4] stuck: until "{{ eq .status \"done\" }}" did not hold in 12 attempt(s)`)
	assert.Contains(t, errOut, "listing 2/4 stuck: until not true after 10/12 attempts, status=pending", "a stalled poll says so without a line per attempt")
	assert.NotContains(t, errOut, "after 1/12")

	assert.Contains(t, errOut, `error: step "listing" [3/4] sso (skipped): response is not JSON`)
	assert.Contains(t, errOut, "Sign in to Example SSO", "the excerpt shows what came back")

	assert.Contains(t, errOut, "listing: 2 of 4 items skipped: stuck, sso")
	assert.Contains(t, errOut, "downloaded 2/2 files")
}

// Without on-error=, the earliest failed element ends the run and is named.
func TestStepFailure_FailNamesTheElement(t *testing.T) {
	noPollSleep(t)
	cfg := clipConfig(t, ``)

	code, _, errOut := execCmdFull(t, cfg, "pull", "ok", "flaky", "--download-dir", t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `error: step "listing" [2/2] flaky: transport "auth-fetch" exited 1`)
	assert.NotContains(t, errOut, "retry", "retries= defaults to none")
	assert.NotContains(t, errOut, "downloaded")
}

// An exhausted poll without skip fails the run with the element named.
func TestStepFailure_ExhaustedPollNamesTheElement(t *testing.T) {
	noPollSleep(t)
	capture := func(_ *Cmd, _, _ string, data any) (string, int) {
		if data.(map[string]any)["item"] == "stuck" {
			return `{"parts":[],"status":"pending"}`, 0
		}
		return `{"status":"done"}`, 0
	}
	steps := []Step{{
		Name:     "listing",
		Over:     "ids",
		Until:    `{{ eq .status "done" }}`,
		Attempts: 3,
		Retries:  1,
		Command:  &Cmd{Shell: true, Template: "true"},
	}}
	data := map[string]any{"ids": []any{"ok", "stuck"}}

	_, err := runSteps(steps, data, map[string]any{}, nil, nil, "", "", capture, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "listing" [2/2] stuck: until`)
	assert.Contains(t, err.Error(), `"status":"pending"`)
}

func TestStepProgress_Line(t *testing.T) {
	p := stepProgress{Step: "listing", Index: 16, Total: 53, Item: "TCLP-1", Attempt: 12, Attempts: 120, Status: "pending"}
	assert.Equal(t, "listing 16/53 TCLP-1  attempt 12/120  status=pending", p.line())
	p.Retry, p.Retries = 1, 3
	assert.Equal(t, "listing 16/53 TCLP-1  attempt 12/120  retry 1/3  status=pending", p.line())
	assert.Equal(t, "listing", stepProgress{Step: "listing"}.line())
}

func TestItemLabel(t *testing.T) {
	assert.Equal(t, "TCLP-1", itemLabel("TCLP-1"))
	assert.Equal(t, "7", itemLabel(int64(7)))
	assert.Equal(t, `{"id":"x"}`, itemLabel(map[string]any{"id": "x"}))
	assert.LessOrEqual(t, displayWidth(itemLabel(string(make([]byte, 200)))), 60)
}

func TestResponseStatus(t *testing.T) {
	assert.Equal(t, "pending", responseStatus(map[string]any{"status": "pending"}))
	assert.Equal(t, "", responseStatus(map[string]any{"state": "x"}))
	assert.Equal(t, "", responseStatus("text"))
}
