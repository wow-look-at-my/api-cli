package main

import (
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The two verdicts <join contiguous=> accepts.
const (
	joinGapWarn  = "warn"
	joinGapError = "error"
)

// Join is the <join> child of a <download>: the one file every member of a
// group becomes. The parts are the transfer's unit and the join is the
// caller's, so the config names both and nothing outside it has to know the
// layout the parts landed in.
type Join struct {
	To      string `json:"to"`
	Cleanup bool   `json:"cleanup,omitempty"`
	// Contiguous reports a gap in the group's numeric order: "warn" logs it,
	// "error" fails the run and writes nothing.
	Contiguous string `json:"contiguous,omitempty"`
}

// buildJoin parses a <join> child. Its attributes are templates over the same
// record the parts are, so one declaration names one output per group.
func buildJoin(n *xnode) (*Join, error) {
	if err := checkAttrs(n, "to", "cleanup", "contiguous"); err != nil {
		return nil, err
	}
	if len(n.Children()) > 0 {
		return nil, fmt.Errorf("<join>: takes no children")
	}
	j := &Join{
		To:         strings.TrimSpace(n.Attr("to")),
		Contiguous: strings.ToLower(strings.TrimSpace(n.Attr("contiguous"))),
	}
	if raw := strings.TrimSpace(n.Attr("cleanup")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("<join>: cleanup=%q must be true or false", raw)
		}
		j.Cleanup = v
	}
	return j, nil
}

// validateJoin checks one declaration's join grammar. group= and order= only
// mean something to a join, so a declaration that carries them without one is
// a config that expects a concatenation nobody asked for.
func validateJoin(d *Download, where string) error {
	if d.Join == nil {
		if d.Group != "" || d.Order != "" {
			return fmt.Errorf("%s: group= and order= describe a <join>, so they need one", where)
		}
		return nil
	}
	if d.Join.To == "" {
		return fmt.Errorf("%s: <join> requires to=", where)
	}
	switch d.Join.Contiguous {
	case "", joinGapWarn, joinGapError:
	default:
		return fmt.Errorf("%s: <join contiguous=%q> must be %q or %q", where, d.Join.Contiguous, joinGapWarn, joinGapError)
	}
	if d.Join.Contiguous != "" && d.Order == "" {
		return fmt.Errorf("%s: <join contiguous=> checks the numbers order= gives, so it needs order=", where)
	}
	if d.To == "" {
		return fmt.Errorf("%s: a joined part needs its own <to>, so each member lands in a file of its own", where)
	}
	return nil
}

// planJoin renders one record's membership: which bucket the file belongs to,
// where that bucket lands, and its place in the order.
//
// order= is read as a number rather than as text, because a capture numbers its
// parts 2, 3, 10 and a lexical sort puts 10 first. A value that is not a number
// fails here: concatenating in the wrong order produces a plausible file that
// is silently wrong, which is worse than no file at all.
func planJoin(d *Download, ctx map[string]any, dir string, idx int) (*joinPart, error) {
	if d.Join == nil {
		return nil, nil
	}
	part := &joinPart{Cleanup: d.Join.Cleanup, Contiguous: d.Join.Contiguous}

	group, err := renderString(d.Group, ctx)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: render group: %w", idx, err)
	}
	part.Group = strings.TrimSpace(group)

	to, err := renderString(d.Join.To, ctx)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: render join to: %w", idx, err)
	}
	if to = strings.TrimSpace(to); to == "" {
		return nil, fmt.Errorf("download[%d]: <join to=> rendered empty", idx)
	}
	part.Dest = underDir(dir, to)

	if d.Order == "" {
		return part, nil
	}
	raw, err := renderString(d.Order, ctx)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: render order: %w", idx, err)
	}
	raw = strings.TrimSpace(raw)
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: order= rendered %q, and a join orders its parts by number", idx, raw)
	}
	part.Order, part.HasOrder = n, true
	return part, nil
}

// joinPart is one file's membership in a concatenation: which output it belongs
// to and where it goes in that output.
type joinPart struct {
	Group      string
	Dest       string
	Order      float64
	HasOrder   bool
	Cleanup    bool
	Contiguous string
}

// joiner concatenates each group of parts into its output. A group joins as
// soon as its own last member lands, so a run of many items writes its first
// output while the rest are still transferring.
//
// Membership is decided at plan time, so the joiner knows every group's size
// before the first transfer starts. It therefore needs no registration step
// racing against a download that finishes first.
type joiner struct {
	log func(string, ...any)

	mu       sync.Mutex
	groups   map[string]*joinGroup
	failures []error
	wg       sync.WaitGroup
}

// joinGroup is one output and the members it still waits for.
type joinGroup struct {
	dest    string
	spec    joinPart
	pending int
	members []*downloadItem
}

// newJoiner counts the membership of every group in a planned run. It returns
// nil when no declaration asked for a join, which keeps that run's fast path
// free of the bookkeeping.
func newJoiner(specs []downloadSpec, log func(string, ...any)) *joiner {
	groups := map[string]*joinGroup{}
	for _, spec := range specs {
		if spec.Join == nil {
			continue
		}
		g, ok := groups[spec.Join.Group]
		if !ok {
			g = &joinGroup{dest: spec.Join.Dest, spec: *spec.Join}
			groups[spec.Join.Group] = g
		}
		g.pending++
	}
	if len(groups) == 0 {
		return nil
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	return &joiner{log: log, groups: groups}
}

// note takes one finished download. The last member of a group starts that
// group's join on a goroutine of its own, so the queue's workers stay on
// transfers.
func (j *joiner) note(item *downloadItem) {
	if j == nil || item.spec.Join == nil {
		return
	}
	j.mu.Lock()
	g := j.groups[item.spec.Join.Group]
	if g == nil {
		j.mu.Unlock()
		return
	}
	g.members = append(g.members, item)
	g.pending--
	ready := g.pending == 0
	j.mu.Unlock()

	if !ready {
		return
	}
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		if err := j.finish(g); err != nil {
			j.mu.Lock()
			j.failures = append(j.failures, err)
			j.mu.Unlock()
		}
	}()
}

// wait blocks until every started join has finished and reports what failed.
func (j *joiner) wait() []error {
	if j == nil {
		return nil
	}
	j.wg.Wait()
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.failures
}

// finish writes one group's output. A group short a member is not written at
// all: half a file that carries the real name reads as a complete capture, and
// nothing downstream can tell the difference.
func (j *joiner) finish(g *joinGroup) error {
	sort.SliceStable(g.members, func(a, b int) bool {
		return g.members[a].spec.Join.Order < g.members[b].spec.Join.Order
	})

	var missing []string
	for _, item := range g.members {
		if item.state.Load() != dlDone {
			missing = append(missing, item.label())
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: not joined, %d of %d part(s) failed: %s",
			g.dest, len(missing), len(g.members), strings.Join(missing, ", "))
	}
	if err := j.checkDest(g); err != nil {
		return err
	}
	if err := j.checkGaps(g); err != nil {
		return err
	}

	if err := concatFiles(g.dest, g.members); err != nil {
		return fmt.Errorf("%s: %w", g.dest, err)
	}
	j.log("joined %s (%d parts)", filepath.Base(g.dest), len(g.members))

	if g.spec.Cleanup {
		for _, item := range g.members {
			os.Remove(item.dest())
		}
		pruneEmptyDirs(g.members)
	}
	return nil
}

// checkDest rejects a group whose members disagree about the output. Two
// records that render one group name and two destinations describe a file that
// cannot exist, and picking one of them silently loses the other's parts.
func (j *joiner) checkDest(g *joinGroup) error {
	for _, item := range g.members {
		if item.spec.Join.Dest != g.dest {
			return fmt.Errorf("group %q: <join to=> rendered both %s and %s", g.spec.Group, g.dest, item.spec.Join.Dest)
		}
	}
	return nil
}

// checkGaps reports a hole in a group's numbering. A capture that lost part 7
// still concatenates into a plausible file, so the numbers are the only place
// the loss is visible.
func (j *joiner) checkGaps(g *joinGroup) error {
	if g.spec.Contiguous == "" {
		return nil
	}
	gaps := missingOrders(g.members)
	if len(gaps) == 0 {
		return nil
	}
	msg := fmt.Sprintf("%s: order is not contiguous, missing %s", g.dest, strings.Join(gaps, ", "))
	if g.spec.Contiguous == joinGapError {
		return fmt.Errorf("%s", msg)
	}
	j.log("warning: %s", msg)
	return nil
}

// missingOrders lists the whole numbers absent between a group's lowest and
// highest order. A fractional order describes no sequence to be missing from,
// so it reports nothing.
func missingOrders(members []*downloadItem) []string {
	present := set.New[int64]()
	var low, high int64
	for i, item := range members {
		n := item.spec.Join.Order
		if n != float64(int64(n)) {
			return nil
		}
		v := int64(n)
		present.Add(v)
		if i == 0 || v < low {
			low = v
		}
		if i == 0 || v > high {
			high = v
		}
	}
	var out []string
	for v := low; v <= high; v++ {
		if !present.Contains(v) {
			out = append(out, strconv.FormatInt(v, 10))
		}
	}
	return out
}

// concatFiles streams the parts into one file, in the order given. It writes a
// .part sibling first, exactly as a transfer does, so an interrupted join never
// leaves a truncated file wearing the output's name.
func concatFiles(dest string, members []*downloadItem) error {
	if dir := filepath.Dir(dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, item := range members {
		if err := appendFile(out, item.dest()); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// appendFile copies one part into the open output. io.Copy streams it, so a
// part larger than memory costs nothing but time.
func appendFile(out io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(out, f)
	return err
}

// pruneEmptyDirs removes the directories the parts left behind, innermost
// first. A join that groups by item usually gives each item a directory, and
// leaving those empty directories behind reads as an incomplete cleanup.
func pruneEmptyDirs(members []*downloadItem) {
	seen := set.New[string]()
	for _, item := range members {
		dir := filepath.Dir(item.dest())
		if dir == "" || dir == "." || seen.Contains(dir) {
			continue
		}
		seen.Add(dir)
		os.Remove(dir)
	}
}
