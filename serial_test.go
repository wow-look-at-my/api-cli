package main

import "testing"

// serial gives one test the process to itself.
//
// The toolchain's testing package runs tests in parallel by default. This
// package's tests swap package-level vars -- execStdout, execStderr,
// execStdin, httpClient, downloadClient, the transport registry and the
// process-wide download queue -- and two tests cannot hold those at once. A
// test that swaps one calls this first, or it reads another test's output.
func serial(t *testing.T) {
	t.Helper()
	t.Serial()
}
