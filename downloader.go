package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
	// Hash is the expected digest as lowercase hex, empty when the declaration
	// supplied none. HashAlgo names the algorithm that produced it.
	Hash     string
	HashAlgo string
	// Transport is the resolved program that fetches this file in place of the
	// built-in client, or nil for the built-in client.
	Transport *downloadTransport
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

// label is the short name the display and the log use: the destination's file
// name once the transfer has resolved it, and the URL's until then.
func (d *downloadItem) label() string {
	if s := d.dest(); s != "" {
		return filepath.Base(s)
	}
	return urlFilename(d.spec.URL)
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
	// errOut receives a <transport> program's stderr. Held per batch rather
	// than read from execStderr on a worker, so the display can swap the
	// process's channels without a worker reading them mid-swap.
	errOut io.Writer

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
// change and errOut a transport program's stderr; pass nil for either only
// where nothing is watching.
func (q *downloadQueue) batch(log func(string, ...any), errOut io.Writer) *downloadBatch {
	if log == nil {
		log = func(string, ...any) {}
	}
	if errOut == nil {
		errOut = io.Discard
	}
	return &downloadBatch{q: q, log: log, errOut: errOut}
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
	// A directory destination is not yet a file name — the response gets to
	// pick that — so the item stays unnamed until the transfer resolves it.
	if spec.DestIsDir {
		item.name.Store("")
	} else {
		item.name.Store(spec.Dest)
	}
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
	log("downloading %s", item.label())

	var err error
	for attempt := 0; ; attempt++ {
		var retryable bool
		err, retryable = q.fetch(item)
		if err == nil {
			item.end.Store(time.Now().UnixNano())
			item.state.Store(dlDone)
			// Name the algorithm on success: a verification you cannot see
			// happen is indistinguishable from one that never ran.
			verified := ""
			if item.spec.Hash != "" {
				verified = ", " + item.spec.HashAlgo + " ok"
			}
			log("downloaded %s (%s%s)", item.dest(), humanBytes(item.done.Load()), verified)
			return
		}
		if !retryable || attempt >= q.retries {
			break
		}
		log("retrying %s: %v", item.label(), err)
		item.done.Store(0)
		time.Sleep(retryDelay)
	}

	item.mu.Lock()
	item.err = err
	item.mu.Unlock()
	item.end.Store(time.Now().UnixNano())
	item.state.Store(dlFailed)
	log("failed %s: %v", item.label(), err)
}

// fetch performs one attempt. The second return reports whether retrying could
// plausibly help: a 404 is the answer, not a hiccup.
func (q *downloadQueue) fetch(item *downloadItem) (error, bool) {
	if item.spec.Transport != nil {
		return q.fetchViaTransport(item)
	}
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

	pf, err := openPart(item, responseFilename(resp, item.spec.URL))
	if err != nil {
		return err, false
	}
	if _, err := io.Copy(pf.sink, resp.Body); err != nil {
		pf.abort()
		return err, true
	}
	return pf.commit()
}

// fetchViaTransport runs the download over a <transport> program, streaming its
// stdout straight into the destination.
//
// Buffering it as a string the way a request-form transport does would put the
// whole file in memory, so this is the one place the two paths differ. What the
// program is handed is identical, and what happens to the bytes afterwards --
// the .part sibling, the byte count, the digest -- is the same code.
func (q *downloadQueue) fetchViaTransport(item *downloadItem) (error, bool) {
	tr := item.spec.Transport
	pf, err := openPart(item, urlFilename(item.spec.URL))
	if err != nil {
		return err, false
	}

	cmd := exec.Command(tr.Argv[0], tr.Argv[1:]...)
	cmd.Dir = tr.Cwd
	cmd.Stdin = strings.NewReader(tr.Stdin)
	cmd.Stdout = pf.sink
	cmd.Stderr = item.batch.errOut

	if err := cmd.Run(); err != nil {
		pf.abort()
		// A program's exit code says nothing this can read: curl answers 22 for
		// a 404 and 7 for a refused connection, and another transport will use
		// its own numbers. So unlike the built-in client, which can tell a 404
		// from a hiccup, this retries and lets the attempt limit end it.
		return fmt.Errorf("transport %q: %w", tr.Name, err), true
	}
	return pf.commit()
}

// partFile is a destination mid-write: the .part sibling, the byte counter, and
// the digest, behind one writer. Both fetch paths write through it, so a
// transport download gets the same guarantees as a built-in one.
type partFile struct {
	item   *downloadItem
	dest   string
	part   string
	file   *os.File
	sink   io.Writer
	digest hash.Hash
}

// openPart creates the .part file for an item. name is the file name to use if
// the declaration named a directory rather than a file.
func openPart(item *downloadItem, name string) (*partFile, error) {
	dest := item.spec.Dest
	if item.spec.DestIsDir {
		dest = filepath.Join(dest, name)
	}
	item.name.Store(dest)

	if dir := filepath.Dir(dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	// Write through a .part sibling so an interrupted transfer never leaves a
	// truncated file wearing the real name.
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return nil, err
	}

	pf := &partFile{item: item, dest: dest, part: part, file: f}
	// Digested on the way past, so verifying costs no second pass over a file
	// that may not fit in memory or in the page cache.
	sink := []io.Writer{f, item}
	if pf.digest = newHasher(item.spec.HashAlgo, item.spec.Hash); pf.digest != nil {
		sink = append(sink, pf.digest)
	}
	pf.sink = io.MultiWriter(sink...)
	return pf, nil
}

// commit closes the file, checks it against its declared digest, and only then
// gives it the real name.
func (p *partFile) commit() (error, bool) {
	if err := p.file.Close(); err != nil {
		os.Remove(p.part)
		return err, false
	}
	if p.digest != nil {
		// The transfer completed, so the same URL will hand back the same bytes:
		// a wrong digest is an answer, not a hiccup, and retrying only wastes
		// the download again. The file never gets the real name.
		if got := hex.EncodeToString(p.digest.Sum(nil)); got != p.item.spec.Hash {
			os.Remove(p.part)
			return fmt.Errorf("%s mismatch: expected %s, got %s", p.item.spec.HashAlgo, p.item.spec.Hash, got), false
		}
	}
	if err := os.Rename(p.part, p.dest); err != nil {
		return err, false
	}
	return nil, false
}

// abort drops a partial write, leaving nothing behind for a retry to trip over.
func (p *partFile) abort() {
	p.file.Close()
	os.Remove(p.part)
}

// newHasher returns the hasher for a spec, or nil when the declaration supplied
// no digest to check against.
func newHasher(algo, want string) hash.Hash {
	if want == "" {
		return nil
	}
	switch algo {
	case "md5":
		return md5.New()
	case "sha1":
		return sha1.New()
	case "sha512":
		return sha512.New()
	default:
		return sha256.New()
	}
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
		state := item.state.Load()
		t.Bytes += done
		switch state {
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
			// Only unfinished work leaves the aggregate open-ended. A finished
			// download's bytes are its final contribution either way, so a run
			// that ended with a failure still reports a settled total.
			if state == dlActive || state == dlQueued {
				t.TotalKnown = false
			}
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
	// TotalIsFloor marks an aggregate whose denominator is incomplete because
	// some download in it has not reported a length.
	TotalIsFloor bool
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
