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

	isTTY, width := stdoutSize()
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
	for _, spec := range specs {
		s.batch.add(spec)
	}
	items := s.batch.wait()
	s.close()

	return reportDownloads(items, time.Since(start), s.tty)
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
	for _, spec := range specs {
		batch.add(spec)
	}

	var out strings.Builder
	failed := 0
	for _, item := range batch.wait() {
		if err := item.failure(); err != nil {
			failed++
			fmt.Fprintf(&out, "failed %s: %v\n", item.spec.URL, err)
			continue
		}
		fmt.Fprintf(&out, "%s (%s)\n", item.dest(), humanBytes(item.done.Load()))
	}
	return strings.TrimRight(log.String()+out.String(), "\n"), failed > 0
}

// stdoutSize reports whether execStdout is a terminal and how wide it is. A
// non-*os.File writer (a buffer in tests, a pipe in a shell) is never a
// terminal, which is exactly the "piped to a file" case.
func stdoutSize() (bool, int) {
	f, ok := execStdout.(*os.File)
	if !ok {
		return false, 0
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return false, 0
	}
	w, _, err := term.GetSize(fd)
	if err != nil || w <= 0 {
		return true, 80
	}
	return true, w
}
