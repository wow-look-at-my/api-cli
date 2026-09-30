package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

// parseProgressLine reads a single report: a JSON object with "done", "total"
// or both. An unknown key, a missing value or a negative number is an error: a
// program that speaks the protocol wrong must hear about it, or its rows sit at
// zero with no reason given.
func parseProgressLine(line string) (progressReport, error) {
	r := progressReport{done: -1, total: -1}
	var wire struct {
		Done  *int64 `json:"done"`
		Total *int64 `json:"total"`
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return r, fmt.Errorf("progress line %q: %w (want {\"done\":N,\"total\":N})", line, err)
	}
	if dec.More() {
		return r, fmt.Errorf("progress line %q: one JSON object per line", line)
	}
	if wire.Done == nil && wire.Total == nil {
		return r, fmt.Errorf("progress line %q names neither done nor total", line)
	}
	for _, v := range []*int64{wire.Done, wire.Total} {
		if v != nil && *v < 0 {
			return r, fmt.Errorf("progress line %q: a byte count cannot be negative", line)
		}
	}
	if wire.Done != nil {
		r.done = *wire.Done
	}
	if wire.Total != nil {
		r.total = *wire.Total
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
