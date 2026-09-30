package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// The progress channel lets a download transport report how far it got.
const (
	progressFD  = 3
	progressEnv = "API_CLI_PROGRESS_FD"
)

// progressReport is a single parsed line.
type progressReport struct {
	done, total int64
}

// parseProgressLine reads a single report. An unknown key, a missing value or
// a negative number is an error: a program that speaks the protocol wrong
// must hear about it, or its rows sit at zero with no reason given.
func parseProgressLine(line string) (progressReport, error) {
	r := progressReport{done: -1, total: -1}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return r, fmt.Errorf("empty progress line")
	}
	for _, f := range fields {
		key, val, ok := strings.Cut(f, "=")
		if !ok {
			return r, fmt.Errorf("progress field %q is not key=value", f)
		}
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n < 0 {
			return r, fmt.Errorf("progress field %q needs a whole number of bytes", f)
		}
		switch key {
		case "done":
			r.done = n
		case "total":
			r.total = n
		default:
			return r, fmt.Errorf("progress field %q: the keys are done and total", f)
		}
	}
	return r, nil
}

// readProgress applies each report to the item until the program closes its
// end of the pipe. It returns the first bad line as an error, and drains the
// rest so the program never blocks on a full pipe.
func readProgress(r io.Reader, item *downloadItem) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		rep, err := parseProgressLine(sc.Text())
		if err != nil {
			_, _ = io.Copy(io.Discard, r)
			return err
		}
		if rep.done >= 0 {
			item.reported.Store(rep.done)
		}
		if rep.total >= 0 {
			item.total.Store(rep.total)
		}
	}
	return sc.Err()
}

// shown is the byte count the display draws. While the transfer is open it is
// the larger of the stdout count and the program's own report. A program that
// never reports is measured from /proc instead.
func (d *downloadItem) shown() int64 {
	done := d.done.Load()
	if d.state.Load() != dlActive {
		return done
	}
	if rep := d.reported.Load(); rep > 0 {
		return max(done, rep)
	}
	return max(done, d.observed.Load())
}

func runWithProgress(cmd *exec.Cmd, item *downloadItem) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	cmd.ExtraFiles = []*os.File{w}
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", progressEnv, progressFD))
	if err := cmd.Start(); err != nil {
		w.Close()
		return err
	}
	w.Close()

	readErr := make(chan error, 1)
	go func() { readErr <- readProgress(r, item) }()
	stop, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		sampleProcRead(cmd.Process.Pid, item, stop)
	}()
	runErr := cmd.Wait()
	close(stop)
	<-sampled
	perr := <-readErr
	if runErr != nil {
		return runErr
	}
	if perr != nil {
		return fmt.Errorf("%w: %v", errBadProgress, perr)
	}
	return nil
}

// errBadProgress marks a protocol violation. The same program sends the same line again, so a retry cannot help.
var errBadProgress = errors.New("bad line on the progress fd")
