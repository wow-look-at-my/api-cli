package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipelineServer stands in for an API whose listing is a job: submit names a
// job, the job answers "pending" once, and the answer after that carries the
// parts. The listing of the named item skips part 2, which is the hole the
// contiguity check exists to report.
func pipelineServer(t *testing.T, missing string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	polls := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasPrefix(r.URL.Path, "/submit/"):
			item := strings.TrimPrefix(r.URL.Path, "/submit/")
			writeJSON(w, map[string]any{"job": "job-" + item, "status": "pending"})

		case strings.HasPrefix(r.URL.Path, "/job/"):
			job := strings.TrimPrefix(r.URL.Path, "/job/")
			item := strings.TrimPrefix(job, "job-")
			mu.Lock()
			polls[job]++
			n := polls[job]
			mu.Unlock()
			if n < 2 {
				writeJSON(w, map[string]any{"status": "pending"})
				return
			}
			var parts []any
			for seq := 1; seq <= 3; seq++ {
				if item == missing && seq == 2 {
					continue
				}
				parts = append(parts, map[string]any{
					"item_id": item,
					"seq":     seq,
					"url":     fmt.Sprintf("%s/part/%s/%d", base, item, seq),
				})
			}
			writeJSON(w, map[string]any{"status": "done", "parts": parts})

		case strings.HasPrefix(r.URL.Path, "/part/"):
			// The body names its own part, so a wrong order is visible in the
			// joined file rather than only in a byte count.
			fmt.Fprintf(w, "[%s]", strings.TrimPrefix(r.URL.Path, "/part/"))

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pipelineConfig is the whole acceptance case: fan out over the arguments,
// poll each listing to completion, queue every part of every item together,
// and leave one joined file per item with no parts behind.
func pipelineConfig(srv *httptest.Server, contiguous string) string {
	return `<config name="pull">
		<downloads concurrency="2"/>
		<command name="pull">
			<arg name="items" variadic="true"/>
			<steps>
				<step name="submit" over="arg.items">
					<run><request><url>` + srv.URL + `/submit/<value name="item"/></url></request></run>
				</step>
				<step name="listing" over="result.submit" until="{{ eq .status &quot;done&quot; }}" interval="1ms" attempts="5">
					<run><request><url>` + srv.URL + `/job/<value name="item.result.job"/></url></request></run>
				</step>
			</steps>
			<download over="{{ toJson (collect &quot;result.parts&quot; .result.listing) }}"
				group="{{ .item_id }}" order="{{ .seq }}">
				<url><value name="url"/></url>
				<to><value name="run.tmpdir"/>/<value name="item_id"/>/<value name="seq"/>.part</to>
				<join to="{{ .item_id }}.bin" cleanup="true" contiguous="` + contiguous + `"/>
			</download>
		</command>
	</config>`
}

func TestIntegration_FanOutPollFetchJoin(t *testing.T) {
	srv := pipelineServer(t, "")
	swapHTTPClient(t, srv)
	swapDownloadClient(t, srv)
	out := t.TempDir()

	cfg, err := loadStr(t, pipelineConfig(srv, "error"))
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "pull", "a1", "b2", "--download-dir", out)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	// One output per item, its parts in numeric order, and nothing else left in
	// the output directory.
	for _, item := range []string{"a1", "b2"} {
		body, rerr := os.ReadFile(filepath.Join(out, item+".bin"))
		require.NoError(t, rerr)
		assert.Equal(t, fmt.Sprintf("[%s/1][%s/2][%s/3]", item, item, item), string(body))
	}
	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"a1.bin", "b2.bin"}, names, "cleanup= leaves no parts")
	assert.Contains(t, errOut, "downloaded 6/6 files")
	assert.Contains(t, errOut, "joined a1.bin (3 parts)")
}

// A gap in the numbering is the sign of a truncated capture, so the run says so
// and writes nothing for that item.
func TestIntegration_JoinReportsAGapInTheOrder(t *testing.T) {
	srv := pipelineServer(t, "b2")
	swapHTTPClient(t, srv)
	swapDownloadClient(t, srv)
	out := t.TempDir()

	cfg, err := loadStr(t, pipelineConfig(srv, "error"))
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "pull", "a1", "b2", "--download-dir", out)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "order is not contiguous, missing 2")
	assert.FileExists(t, filepath.Join(out, "a1.bin"), "one broken item does not spoil the other")
	assert.NoFileExists(t, filepath.Join(out, "b2.bin"))
}

// warn= reports the same gap and still writes the file: a caller who knows the
// source skips numbers asked for the join anyway.
func TestIntegration_JoinWarnsAboutAGapAndStillWrites(t *testing.T) {
	srv := pipelineServer(t, "b2")
	swapHTTPClient(t, srv)
	swapDownloadClient(t, srv)
	out := t.TempDir()

	cfg, err := loadStr(t, pipelineConfig(srv, "warn"))
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "pull", "b2", "--download-dir", out)
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Contains(t, errOut, "order is not contiguous, missing 2")
	body, err := os.ReadFile(filepath.Join(out, "b2.bin"))
	require.NoError(t, err)
	assert.Equal(t, "[b2/1][b2/3]", string(body))
}

// A group short a part is not written at all. Half a capture wearing the real
// name is indistinguishable from a whole one.
func TestIntegration_JoinSkipsAGroupWithAFailedPart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/part/2" {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, "<%s>", strings.TrimPrefix(r.URL.Path, "/part/"))
	}))
	t.Cleanup(srv.Close)
	swapDownloadClient(t, srv)
	out := t.TempDir()

	cfg, err := loadStr(t, `<config name="j"><command name="pull">
		<download over="{{ toJson (list 1 2 3) }}" order="{{ .item }}" group="one">
			<url>`+srv.URL+`/part/<value name="item"/></url>
			<to><value name="run.tmpdir"/>/<value name="item"/>.part</to>
			<join to="whole.bin" cleanup="true"/>
		</download>
	</command></config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "pull", "--download-dir", out, "--concurrency", "1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "part(s) failed")
	assert.NoFileExists(t, filepath.Join(out, "whole.bin"))
}
