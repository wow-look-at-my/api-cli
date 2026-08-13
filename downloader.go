package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// retryDelay separates attempts at a fixed cadence: a slow origin is not a
// reason to wait exponentially longer for it. A var so tests need not sleep.
var retryDelay = time.Second

// downloadClient performs the transfers. Deliberately not httpClient: that one
// caps a whole request at 60s, which is a sane API deadline and a hard ceiling
// on file size for a downloader. Here the deadlines cover connecting and the
// wait for headers, and the body then takes as long as it takes.
var downloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// jobBuffer is the enqueue channel's depth. Past it, adding blocks until a
// worker picks something up — backpressure, never a dropped file.
const jobBuffer = 1024

// download states, stored as an int32 so the renderer can read them without
// taking a lock.
const (
	dlQueued int32 = iota
	dlActive
	dlDone
	dlFailed
)

// downloadSpec is one fully-rendered hand-off: where to fetch from, where to
// put it, and the auth the config worked out for it.
type downloadSpec struct {
	URL     string
	Dest    string
	Headers []renderedHeader
	// DestIsDir means Dest names a directory and the file name still has to
	// come from the URL or the response's Content-Disposition.
	DestIsDir bool
}

// downloadItem is a spec plus its live progress. Every mutable field is atomic
// so the renderer can sample a frame while workers are mid-transfer.
type downloadItem struct {
	spec  downloadSpec
	batch *downloadBatch

	name  atomic.Value // string: the destination path, once known
	done  atomic.Int64
	total atomic.Int64 // -1 until the server reports a length
	state atomic.Int32
	start atomic.Int64 // unix nanos
	end   atomic.Int64 // unix nanos

	mu  sync.Mutex
	err error
}

func (d *downloadItem) label() string {
	if s := d.dest(); s != "" {
		return filepath.Base(s)
	}
	return path.Base(d.spec.URL)
}

func (d *downloadItem) dest() string {
	s, _ := d.name.Load().(string)
	return s
}

func (d *downloadItem) failure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Write counts bytes as they land. The worker tees the response body through
// the item, so progress needs no polling of the file.
func (d *downloadItem) Write(p []byte) (int, error) {
	d.done.Add(int64(len(p)))
	return len(p), nil
}

// downloadQueue is the shared downloader: one worker pool for the process,
// however many hand-offs feed it. Workers live for the process lifetime, so an
// MCP server that serves many tool calls reuses the same pool.
type downloadQueue struct {
	concurrency int
	retries     int
	client      *http.Client
	jobs        chan *downloadItem
}

// downloadBatch is one run's worth of work on the shared queue: the items an
// invocation enqueued, and where that invocation's log lines go. Batches share
// the pool, so concurrency is a property of the queue, not of a batch.
type downloadBatch struct {
	q   *downloadQueue
	log func(format string, args ...any)

	wg    sync.WaitGroup
	mu    sync.Mutex
	items []*downloadItem
}

// theQueue is the process-wide queue. A hand-off is meant to reach one central
// downloader, so every enqueue in a process lands in the same pool rather than
// in a pool per declaration.
var theQueue *downloadQueue

// sharedQueue returns the process-wide queue, creating it on first use with the
// given settings. Later calls reuse the running pool: one process loads one
// config, so concurrency is decided once.
func sharedQueue(concurrency, retries int) *downloadQueue {
	if theQueue == nil {
		theQueue = newDownloadQueue(concurrency, retries)
	}
	return theQueue
}

// resetSharedQueue drops the process-wide queue so the next caller builds a
// fresh pool. Tests use it between runs; production never does.
func resetSharedQueue() { theQueue = nil }

func newDownloadQueue(concurrency, retries int) *downloadQueue {
	if concurrency < 1 {
		concurrency = 1
	}
	if retries < 0 {
		retries = 0
	}
	q := &downloadQueue{
		concurrency: concurrency,
		retries:     retries,
		client:      downloadClient,
		jobs:        make(chan *downloadItem, jobBuffer),
	}
	for i := 0; i < concurrency; i++ {
		go q.worker()
	}
	return q
}

// batch opens a unit of work on the queue. log receives one line per state
// change; pass a no-op only where nothing is watching.
func (q *downloadQueue) batch(log func(string, ...any)) *downloadBatch {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &downloadBatch{q: q, log: log}
}

func (q *downloadQueue) worker() {
	for item := range q.jobs {
		q.run(item)
		item.batch.wg.Done()
	}
}

// add enqueues a spec. Workers pick it up immediately, so a declaration that
// renders slowly does not hold up transfers already planned.
func (b *downloadBatch) add(spec downloadSpec) *downloadItem {
	item := &downloadItem{spec: spec, batch: b}
	item.total.Store(-1)
	item.name.Store(spec.Dest)
	b.mu.Lock()
	b.items = append(b.items, item)
	b.mu.Unlock()
	b.wg.Add(1)
	b.q.jobs <- item
	return item
}

// wait blocks until every item in this batch has finished, then returns them in
// enqueue order.
func (b *downloadBatch) wait() []*downloadItem {
	b.wg.Wait()
	return b.snapshot()
}

// snapshot copies the item list for the renderer. The items themselves are
// shared — their progress fields are atomic and meant to be read live.
func (b *downloadBatch) snapshot() []*downloadItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*downloadItem, len(b.items))
	copy(out, b.items)
	return out
}

// run performs one download, retrying transient failures at a fixed cadence.
// Every outcome is recorded on the item: a failure is never a silent skip.
func (q *downloadQueue) run(item *downloadItem) {
	log := item.batch.log
	item.state.Store(dlActive)
	item.start.Store(time.Now().UnixNano())
	log("downloading %s", item.spec.URL)

	var err error
	for attempt := 0; ; attempt++ {
		var retryable bool
		err, retryable = q.fetch(item)
		if err == nil {
			item.end.Store(time.Now().UnixNano())
			item.state.Store(dlDone)
			log("downloaded %s (%s)", item.dest(), humanBytes(item.done.Load()))
			return
		}
		if !retryable || attempt >= q.retries {
			break
		}
		log("retrying %s: %v", item.spec.URL, err)
		item.done.Store(0)
		time.Sleep(retryDelay)
	}

	item.mu.Lock()
	item.err = err
	item.mu.Unlock()
	item.end.Store(time.Now().UnixNano())
	item.state.Store(dlFailed)
	log("failed %s: %v", item.spec.URL, err)
}

// fetch performs one attempt. The second return reports whether retrying could
// plausibly help: a 404 is the answer, not a hiccup.
func (q *downloadQueue) fetch(item *downloadItem) (error, bool) {
	req, err := http.NewRequest(http.MethodGet, item.spec.URL, nil)
	if err != nil {
		return err, false
	}
	for _, h := range item.spec.Headers {
		req.Header.Set(h.Name, h.Value)
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return err, true
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %s", strings.TrimSpace(resp.Status)), retryableStatus(resp.StatusCode)
	}
	if resp.ContentLength >= 0 {
		item.total.Store(resp.ContentLength)
	}

	dest := item.spec.Dest
	if item.spec.DestIsDir {
		dest = filepath.Join(dest, responseFilename(resp, item.spec.URL))
	}
	item.name.Store(dest)

	if dir := filepath.Dir(dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err, false
		}
	}

	// Write through a .part sibling so an interrupted transfer never leaves a
	// truncated file wearing the real name.
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err, false
	}
	_, copyErr := io.Copy(io.MultiWriter(f, item), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(part)
		return copyErr, true
	}
	if closeErr != nil {
		os.Remove(part)
		return closeErr, false
	}
	if err := os.Rename(part, dest); err != nil {
		return err, false
	}
	return nil, false
}

// retryableStatus reports whether an error status is worth another attempt:
// server-side faults and the two "come back later" client statuses.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return code >= 500
}

// responseFilename picks a file name for a destination that named a directory:
// the server's Content-Disposition when it offers one, else the URL's last path
// segment, else a fixed fallback so the file still lands somewhere.
func responseFilename(resp *http.Response, rawURL string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := sanitizeFilename(params["filename"]); name != "" {
				return name
			}
		}
	}
	return urlFilename(rawURL)
}

// urlFilename derives a file name from a URL's path.
func urlFilename(rawURL string) string {
	name := ""
	if u, err := url.Parse(rawURL); err == nil {
		name = path.Base(u.Path)
	}
	if name = sanitizeFilename(name); name != "" {
		return name
	}
	return "download"
}

// sanitizeFilename reduces an untrusted name to a bare file name. A server that
// answers with "../../etc/passwd" gets to name the file, not to place it.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	return name
}

// downloadTotals is one frame's worth of aggregate progress.
type downloadTotals struct {
	Active, Queued, Done, Failed int
	Bytes, Total                 int64
	// TotalKnown is false when any download has not reported a length, which
	// makes the aggregate total a floor rather than a target.
	TotalKnown bool
	Elapsed    time.Duration
}

// tallyDownloads aggregates the item list for the progress display.
func tallyDownloads(items []*downloadItem, now time.Time) downloadTotals {
	t := downloadTotals{TotalKnown: true}
	earliest := int64(0)
	for _, item := range items {
		done := item.done.Load()
		total := item.total.Load()
		t.Bytes += done
		switch item.state.Load() {
		case dlActive:
			t.Active++
		case dlDone:
			t.Done++
		case dlFailed:
			t.Failed++
		default:
			t.Queued++
		}
		if total < 0 {
			t.TotalKnown = false
			t.Total += done
		} else {
			t.Total += total
		}
		if s := item.start.Load(); s > 0 && (earliest == 0 || s < earliest) {
			earliest = s
		}
	}
	if earliest > 0 {
		t.Elapsed = now.Sub(time.Unix(0, earliest))
	}
	return t
}

// itemProgress is one download's derived numbers: what the display needs and
// nothing it would have to recompute.
type itemProgress struct {
	Done, Total int64
	Fraction    float64 // -1 when the total is unknown
	Speed       float64 // bytes/sec, 0 when not measurable yet
	ETA         time.Duration
	HasETA      bool
}

// progressOf derives an item's rate and ETA from the bytes it has moved since
// it started.
func progressOf(done, total int64, start, now time.Time) itemProgress {
	p := itemProgress{Done: done, Total: total, Fraction: -1}
	if total > 0 {
		p.Fraction = float64(done) / float64(total)
		if p.Fraction > 1 {
			p.Fraction = 1
		}
	}
	elapsed := now.Sub(start).Seconds()
	if start.IsZero() || elapsed <= 0 || done <= 0 {
		return p
	}
	p.Speed = float64(done) / elapsed
	if total > done && p.Speed > 0 {
		p.ETA = time.Duration(float64(total-done) / p.Speed * float64(time.Second))
		p.HasETA = true
	}
	return p
}
