package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// downloadSession is one invocation's use of the shared downloader: the batch
// it enqueues into, and the display it runs while that batch drains.
//
// The session opens before the leaf's steps run, not after, because the steps
// are what work the URL out — their output belongs in the same log region as
// the downloader's.
type downloadSession struct {
	settings downloadSettings
	batch    *downloadBatch
	tui      *tui
	prevOut  io.Writer
	prevErr  io.Writer
	tty      bool
	closed   bool
}

// startDownloadSession resolves settings, claims a batch on the shared queue,
// and — on a terminal — takes over the screen. When stdout is redirected there
// is no display: output stays line-based so it survives a pipe.
func startDownloadSession(c *cobra.Command) *downloadSession {
	s := &downloadSession{settings: resolveDownloadSettings(c)}
	q := sharedQueue(s.settings.Concurrency, s.settings.Retries)

	isTTY, width, _ := stdoutSize()
	s.tty = isTTY
	if !isTTY || s.settings.NoTUI {
		errOut := execStderr
		s.batch = q.batch(func(format string, args ...any) {
			fmt.Fprintf(errOut, format+"\n", args...)
		}, errOut)
		return s
	}

	s.prevOut, s.prevErr = execStdout, execStderr
	s.batch = q.batch(nil, nil)
	s.tui = newTUI(s.prevOut, width, s.batch.snapshot)
	s.batch.log = s.tui.logf
	// A transport program's stderr scrolls above the slots too.
	s.batch.errOut = s.tui
	// Steps and the downloader write above the slots rather than over them.
	execStdout, execStderr = s.tui, s.tui
	s.tui.Start()
	return s
}

// close ends the display and restores the output channels. Idempotent: the
// happy path closes before printing its summary, and the caller defers it for
// every other way out of the leaf.
func (s *downloadSession) close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.tui == nil {
		return
	}
	s.tui.Stop()
	execStdout, execStderr = s.prevOut, s.prevErr
}

// run plans the leaf's declarations, hands them to the queue, and waits for the
// queue to drain. Returns the leaf's exit code: non-zero if any file failed.
func (s *downloadSession) run(dls []Download, data map[string]any) int {
	specs, err := planDownloads(dls, data, s.settings.Dir)
	if err != nil {
		s.close()
		fmt.Fprintln(execStderr, "error:", err)
		return 1
	}
	if len(specs) == 0 {
		s.close()
		fmt.Fprintln(execStderr, "no downloads: every <download> was skipped or matched nothing")
		return 0
	}

	logVerbose("downloads: %d queued at concurrency %d", len(specs), s.settings.Concurrency)
	start := time.Now()
	j := newJoiner(specs, s.batch.log)
	s.batch.onDone = j.note
	for _, spec := range specs {
		s.batch.add(spec)
	}
	items := s.batch.wait()
	joinErrs := j.wait()
	s.close()

	code := reportDownloads(items, time.Since(start), s.tty)
	return reportJoins(joinErrs, code)
}

// reportJoins writes what the joiner could not write. A failed join is the
// run's outcome even when every transfer succeeded: the caller asked for one
// file per group, and a group short a part never became one.
func reportJoins(errs []error, code int) int {
	for _, err := range errs {
		fmt.Fprintln(execStderr, "error:", err)
	}
	if len(errs) > 0 {
		return 1
	}
	return code
}

// reportDownloads writes the run's outcome. On a terminal the display already
// showed each file, so only the summary follows it; through a pipe the
// destination paths go to stdout, one per line, for whatever reads them next.
func reportDownloads(items []*downloadItem, elapsed time.Duration, tty bool) int {
	var bytes int64
	ok, failed := 0, 0
	for _, item := range items {
		if item.state.Load() == dlDone {
			ok++
			bytes += item.done.Load()
			if !tty {
				fmt.Fprintln(execStdout, item.dest())
			}
			continue
		}
		failed++
	}

	fmt.Fprintf(execStderr, "downloaded %d/%d files (%s) in %s\n",
		ok, len(items), humanBytes(bytes), elapsed.Round(time.Millisecond))
	if failed == 0 {
		return 0
	}
	for _, item := range items {
		if err := item.failure(); err != nil {
			fmt.Fprintf(execStderr, "error: %s: %v\n", item.spec.URL, err)
		}
	}
	return 1
}

// mcpRunDownloads performs a leaf's downloads for an MCP tool call and reports
// what landed. There is no terminal here, so the log lines are collected and
// returned with the summary.
func mcpRunDownloads(dls []Download, data map[string]any) (string, bool) {
	settings := resolveDownloadSettings(nil)
	var log strings.Builder
	batch := sharedQueue(settings.Concurrency, settings.Retries).batch(func(format string, args ...any) {
		fmt.Fprintf(&log, format+"\n", args...)
	}, &log)

	specs, err := planDownloads(dls, data, settings.Dir)
	if err != nil {
		return "error: " + err.Error(), true
	}
	if len(specs) == 0 {
		return "no downloads: every <download> was skipped or matched nothing", false
	}
	j := newJoiner(specs, batch.log)
	batch.onDone = j.note
	for _, spec := range specs {
		batch.add(spec)
	}

	var out strings.Builder
	failed := 0
	items := batch.wait()
	for _, err := range j.wait() {
		failed++
		fmt.Fprintf(&out, "join failed: %v\n", err)
	}
	for _, item := range items {
		if err := item.failure(); err != nil {
			failed++
			fmt.Fprintf(&out, "failed %s: %v\n", item.spec.URL, err)
			continue
		}
		fmt.Fprintf(&out, "%s (%s)\n", item.dest(), humanBytes(item.done.Load()))
	}
	return strings.TrimRight(log.String()+out.String(), "\n"), failed > 0
}

// openScratch gives the run a working directory of its own, published as
// `.run.tmpdir`. Parts of a join live there until the join writes the output,
// so nothing outside the config has to make a directory and pass it in.
//
// The returned function removes the directory. A run that keeps its parts (a
// join without cleanup=, or no join at all) writes them under the download
// directory instead, which this never touches.
func openScratch(data map[string]any) (func(), error) {
	dir, err := os.MkdirTemp("", "api-cli-")
	if err != nil {
		return nil, fmt.Errorf("scratch directory: %w", err)
	}
	logVerbose("run: scratch directory %s", dir)
	data["run"] = map[string]any{"tmpdir": dir}
	return func() { os.RemoveAll(dir) }, nil
}

// stdoutSize reports whether execStdout is a terminal and how wide it is. A
// non-*os.File writer (a buffer in tests, a pipe in a shell) is never a
// terminal, which is exactly the "piped to a file" case.
func stdoutSize() (bool, int, int) {
	if ttyOverride != nil {
		return true, ttyOverride.width, ttyOverride.height
	}
	f, ok := execStdout.(*os.File)
	if !ok {
		return false, 0, 0
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return false, 0, 0
	}
	w, h, err := term.GetSize(fd)
	if err != nil || w <= 0 || h <= 0 {
		return true, 80, 24
	}
	return true, w, h
}
