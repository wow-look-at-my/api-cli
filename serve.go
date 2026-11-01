// serve.go holds --server: it runs a config on behalf of a --client, on this side of a sandbox.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wow-look-at-my/go-containers/set"
)

// servedRequest is the first line a client sends. Its stdin, stdout and stderr travel with it as SCM_RIGHTS.
type servedRequest struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
}

// servedControl is a later line from the client: a signal to pass on to the run.
type servedControl struct {
	Signal int `json:"signal"`
}

// servedResult is the server's last line for one run.
type servedResult struct {
	Exit  int    `json:"exit"`
	Error string `json:"error,omitempty"`
}

// serverOnlyFlags are the global flags a client may not send. --config swaps the contract. --var sets the server's environment.
var serverOnlyFlags = set.Of("config", "var", "debug", "verbose")

// maxRequestBytes bounds the first line of a client.
const maxRequestBytes = 1 << 20

// refuseServerOnlyFlags reports a global flag the client may not send. Everything after a bare "--" is an argument, not a flag.
func refuseServerOnlyFlags(argv []string) error {
	for _, arg := range argv {
		if arg == "--" {
			return nil
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if serverOnlyFlags.Contains(name) {
			return fmt.Errorf("--%s is not allowed from a client", name)
		}
	}
	return nil
}

// runServer serves cfgPath on the unix socket at sockPath until a signal stops it, or until stdin closes when stdin is a pipe.
func runServer(cfgPath, sockPath string, errOut io.Writer) int {
	absConfig, err := filepath.Abs(cfgPath)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(errOut, "error: cannot find this executable:", err)
		return 2
	}
	ln, err := listenUnix(sockPath)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}
	defer os.Remove(sockPath)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-stop
		_ = ln.Close()
	}()
	if stdinIsPipe() {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			_ = ln.Close()
		}()
	}
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return 0
			}
			fmt.Fprintln(errOut, "error: accept:", err)
			return 1
		}
		go serveConn(conn, exe, absConfig)
	}
}

// listenUnix binds sockPath, owner-only. A stale socket from a dead server is removed first. Any other file at the path is an error.
func listenUnix(sockPath string) (*net.UnixListener, error) {
	if info, err := os.Lstat(sockPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", sockPath)
		}
		if conn, err := net.Dial("unix", sockPath); err == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("a server already listens on %s", sockPath)
		}
		if err := os.Remove(sockPath); err != nil {
			return nil, err
		}
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// stdinIsPipe reports whether stdin is a pipe. A parent that holds the write end ends the server by closing it.
func stdinIsPipe() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeNamedPipe != 0
}

// serveConn answers one client: it runs exe against the config with the client's argv, cwd and stdio.
func serveConn(conn *net.UnixConn, exe, cfgPath string) {
	defer conn.Close()
	reply := func(result servedResult) {
		body, _ := json.Marshal(result)
		_, _ = conn.Write(append(body, '\n'))
	}
	line, files, err := readRequest(conn)
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	if err != nil {
		reply(servedResult{Exit: 2, Error: err.Error()})
		return
	}
	var req servedRequest
	if err := json.Unmarshal(line, &req); err != nil {
		reply(servedResult{Exit: 2, Error: "malformed request: " + err.Error()})
		return
	}
	if err := refuseServerOnlyFlags(req.Argv); err != nil {
		reply(servedResult{Exit: 2, Error: err.Error()})
		return
	}
	if info, err := os.Stat(req.Cwd); err != nil || !info.IsDir() || !filepath.IsAbs(req.Cwd) {
		reply(servedResult{Exit: 2, Error: fmt.Sprintf("working directory %q is not an absolute directory on the server", req.Cwd)})
		return
	}
	cmd := exec.Command(exe, append([]string{"--config", cfgPath}, req.Argv...)...)
	cmd.Dir = req.Cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = files[0], files[1], files[2]
	if err := cmd.Start(); err != nil {
		reply(servedResult{Exit: 2, Error: err.Error()})
		return
	}
	go relayControl(conn, cmd.Process)
	reply(servedResult{Exit: exitStatus(cmd.Wait())})
}

// readRequest reads the first line and the descriptors that come with it.
func readRequest(conn *net.UnixConn) ([]byte, []*os.File, error) {
	buf := make([]byte, 64<<10)
	oob := make([]byte, syscall.CmsgSpace(3*4))
	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, nil, fmt.Errorf("read request: %w", err)
	}
	files, err := parseRights(oob[:oobn])
	if err != nil {
		return nil, files, err
	}
	line := append([]byte(nil), buf[:n]...)
	reader := bufio.NewReader(conn)
	for !strings.Contains(string(line), "\n") {
		if len(line) > maxRequestBytes {
			return nil, files, errors.New("request line is too long")
		}
		more, err := reader.ReadBytes('\n')
		line = append(line, more...)
		if err != nil {
			return nil, files, fmt.Errorf("read request: %w", err)
		}
	}
	head, _, _ := strings.Cut(string(line), "\n")
	return []byte(head), files, nil
}

// parseRights turns the control message into files: stdin, stdout and stderr.
func parseRights(oob []byte) ([]*os.File, error) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("read descriptors: %w", err)
	}
	var files []*os.File
	for _, msg := range msgs {
		fds, err := syscall.ParseUnixRights(&msg)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "client"))
		}
	}
	if len(files) != 3 {
		return files, fmt.Errorf("the request carried %d descriptors, not 3 (stdin, stdout, stderr)", len(files))
	}
	return files, nil
}

// relayControl passes each signal the client sends on to the run. A client that goes away kills the run.
func relayControl(conn *net.UnixConn, proc *os.Process) {
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var ctl servedControl
		if json.Unmarshal(scanner.Bytes(), &ctl) == nil && ctl.Signal > 0 {
			_ = proc.Signal(syscall.Signal(ctl.Signal))
		}
	}
	_ = proc.Kill()
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 1
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return exitErr.ExitCode()
}
