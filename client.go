// client.go holds --client: it hands a command line to a --server and takes its exit code.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// clientTarget reports the socket of a leading --client. The flag must come first, so every later argument goes to the server as it is.
func clientTarget(argv []string) (sock string, rest []string, ok bool, err error) {
	if len(argv) == 0 {
		return "", nil, false, nil
	}
	if value, found := strings.CutPrefix(argv[0], "--client="); found {
		return value, argv[1:], true, nil
	}
	if argv[0] != "--client" {
		return "", nil, false, nil
	}
	if len(argv) < 2 {
		return "", nil, true, fmt.Errorf("--client needs a socket path")
	}
	return argv[1], argv[2:], true, nil
}

// runClient runs argv on the server at sock. The server gets this process's stdin, stdout and stderr, so the output streams straight to them.
func runClient(sock string, argv []string, stdin, stdout, stderr *os.File, errOut io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		fmt.Fprintf(errOut, "error: no api-cli server answers on %s: %v\n", sock, err)
		return 2
	}
	defer conn.Close()
	line, err := json.Marshal(servedRequest{Argv: argv, Cwd: cwd})
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}
	rights := syscall.UnixRights(int(stdin.Fd()), int(stdout.Fd()), int(stderr.Fd()))
	if _, _, err := conn.WriteMsgUnix(append(line, '\n'), rights, nil); err != nil {
		fmt.Fprintln(errOut, "error: send request:", err)
		return 2
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			body, _ := json.Marshal(servedControl{Signal: int(sig.(syscall.Signal))})
			_, _ = conn.Write(append(body, '\n'))
		}
	}()
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(errOut, "error: the server on %s closed the connection with no result: %v\n", sock, err)
		return 2
	}
	var result servedResult
	if err := json.Unmarshal(reply, &result); err != nil {
		fmt.Fprintln(errOut, "error: malformed result from the server:", err)
		return 2
	}
	if result.Error != "" {
		fmt.Fprintln(errOut, "error:", result.Error)
	}
	return result.Exit
}
