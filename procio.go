package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"
)

// A transport that holds the body until it exits writes nothing to stdout, and may never write to the progress fd.

const procSampleInterval = 250 * time.Millisecond

// procRead returns the bytes pid and its live descendants have read, and false
// when /proc cannot answer.
func procRead(pid int) (int64, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	children := map[int][]int{}
	for _, e := range entries {
		child, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if ppid, ok := procParent(child); ok {
			children[ppid] = append(children[ppid], child)
		}
	}
	var total int64
	found := false
	queue := []int{pid}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if n, ok := procRchar(p); ok {
			total += n
			found = true
		}
		queue = append(queue, children[p]...)
	}
	return total, found
}

// procParent reads the ppid from /proc/<pid>/stat. The command name sits in
// parentheses and can hold spaces, so the fields start after the last ")".
func procParent(pid int) (int, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	return ppid, err == nil
}

func procRchar(pid int) (int64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/io")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "rchar:"); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// sampleProcRead stores the largest read count seen for pid on the item until
// stop closes. A child that exits takes its count with it, so the value only
// ever grows.
func sampleProcRead(pid int, item *downloadItem, stop <-chan struct{}) {
	t := time.NewTicker(procSampleInterval)
	defer t.Stop()
	for {
		if n, ok := procRead(pid); ok && n > item.observed.Load() {
			item.observed.Store(n)
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}
