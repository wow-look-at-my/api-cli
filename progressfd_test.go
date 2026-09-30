package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProgressLine(t *testing.T) {
	r, err := parseProgressLine("done=1024 total=4096")
	require.NoError(t, err)
	assert.Equal(t, progressReport{done: 1024, total: 4096}, r)

	r, err = parseProgressLine("total=10")
	require.NoError(t, err)
	assert.Equal(t, progressReport{done: -1, total: 10}, r, "a size known before any byte is a report")

	for _, bad := range []string{"", "bytes=5", "done=", "done=-1", "done=1.5", "done 5"} {
		_, err := parseProgressLine(bad)
		assert.Error(t, err, "%q must be rejected", bad)
	}
}

// A program that holds the body until it exits writes nothing to stdout
// mid-transfer.
func TestTransport_ProgressFDDrivesTheRowWhileStdoutIsSilent(t *testing.T) {
	q := newDownloadQueue(1, 0)
	batch := q.batch(nil, nil)
	item := batch.add(downloadSpec{
		URL:  "https://internal.example/big.bin",
		Dest: filepath.Join(t.TempDir(), "big.bin"),
		Transport: &downloadTransport{Name: "buffered", Argv: []string{"sh", "-c",
			`echo "done=1000 total=5000" >&"$API_CLI_PROGRESS_FD"; sleep 1; head -c 5000 /dev/zero`}},
	})

	deadline := time.Now().Add(3 * time.Second)
	for item.reported.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.Equal(t, dlActive, item.state.Load(), "the report must land while the transfer is open")
	assert.EqualValues(t, 0, item.done.Load(), "stdout has carried nothing yet")
	assert.EqualValues(t, 1000, item.shown(), "the row shows the program's report")
	assert.EqualValues(t, 5000, item.total.Load(), "the program's total gives the row a length")

	batch.wait()
	require.NoError(t, item.failure())
	assert.EqualValues(t, 5000, item.shown(), "the finished count is the file's own size")
}

func TestTransport_BadProgressLineFailsWithoutRetry(t *testing.T) {
	var logs []string
	q := newDownloadQueue(1, 3)
	batch := q.batch(func(f string, a ...any) { logs = append(logs, f) }, nil)
	item := batch.add(downloadSpec{
		URL:       "https://internal.example/x",
		Dest:      filepath.Join(t.TempDir(), "x"),
		Transport: &downloadTransport{Name: "chatty", Argv: []string{"sh", "-c", `echo "bytes=5" >&3; printf x`}},
	})
	batch.wait()

	err := item.failure()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "progress fd")
	assert.Contains(t, err.Error(), "the keys are done and total")
	for _, l := range logs {
		assert.False(t, strings.HasPrefix(l, "retrying"), "a protocol error repeats on every attempt")
	}
	_, statErr := os.Stat(item.spec.Dest)
	assert.True(t, os.IsNotExist(statErr), "a failed transfer leaves no file under the real name")
}

func TestTransport_ProgramThatNeverReportsStillCounts(t *testing.T) {
	q := newDownloadQueue(1, 0)
	batch := q.batch(nil, nil)
	item := batch.add(downloadSpec{
		URL:       "https://internal.example/y",
		Dest:      filepath.Join(t.TempDir(), "y"),
		Transport: &downloadTransport{Name: "plain", Argv: []string{"sh", "-c", `printf hello`}},
	})
	batch.wait()
	require.NoError(t, item.failure())
	assert.EqualValues(t, 5, item.shown())
}

func TestDownloadTransport_ProgressFDIsInTheTemplate(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadStr(t, `<config name="dl">
		<transports>
			<transport name="corp">
				<run>
					<argv>/bin/sh</argv>
					<argv>-c</argv>
					<argv>printf '%s %s' "$0" "$API_CLI_PROGRESS_FD"</argv>
					<argv><value name="request.progress_fd"/></argv>
				</run>
			</transport>
		</transports>
		<command name="grab">
			<download transport="corp">
				<url>https://internal.example/thing</url>
				<to>out.txt</to>
			</download>
		</command>
	</config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "3 3", string(got), "argv and the environment name the same fd")
}
