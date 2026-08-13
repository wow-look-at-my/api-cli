package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Defaults for the shared queue. Concurrency is overridable in the config
// (<downloads concurrency=>) and on the command line (--concurrency).
const (
	defaultConcurrency = 4
	defaultRetries     = 3
	// maxLogLines caps the auto-sized log region; the region also never takes
	// more than half the terminal.
	maxLogLines = 15
)

// Downloads is the top-level <downloads> element: settings for the process-wide
// download queue. One queue serves the whole run, so these are set once.
type Downloads struct {
	Concurrency int    `json:"concurrency,omitempty"`
	Retries     int    `json:"retries,omitempty"`
	Dir         string `json:"dir,omitempty"`
	LogLines    int    `json:"logLines,omitempty"`
}

// Download is one hand-off on a leaf: the URL a step worked out, where to put
// it, and the auth that reaches it. Every string is a template rendered against
// the leaf's full data context (.arg, .flag, .env, .var, .entry, .result), so a
// URL parsed out of an earlier step is just `.result.<step>.<path>`.
//
// With `over=`, the declaration repeats per record in that list: the record's
// keys are promoted to the top level (like a <field expr=>) and the record
// itself is available as `.item`.
type Download struct {
	Over    string   `json:"over,omitempty"`
	When    string   `json:"when,omitempty"`
	URL     string   `json:"url"`
	To      string   `json:"to,omitempty"`
	Headers []Header `json:"headers,omitempty"`
	Cookies []Header `json:"cookies,omitempty"`
}

// downloadSettings is the effective configuration for one run: config values
// with the command line layered on top.
type downloadSettings struct {
	Concurrency int
	Retries     int
	Dir         string
	LogLines    int
	NoTUI       bool
}

// downloadDefaults holds the active config's <downloads> settings. Published by
// installDownloads alongside the transport registry, for the same reason: it is
// config data that every leaf may need and no call site would do anything with
// but pass along.
var downloadDefaults *Downloads

// installDownloads publishes a config's <downloads> settings. Called by every
// path that turns a config into runnable commands (newRoot, buildMCPServer).
func installDownloads(cfg *Config) {
	downloadDefaults = nil
	if cfg != nil {
		downloadDefaults = cfg.Downloads
	}
}

// resolveDownloadSettings layers the command line over the config. c may be nil
// (the MCP path has no cobra command), in which case the config stands alone.
func resolveDownloadSettings(c *cobra.Command) downloadSettings {
	s := downloadSettings{Concurrency: defaultConcurrency, Retries: defaultRetries, Dir: "."}
	if d := downloadDefaults; d != nil {
		if d.Concurrency > 0 {
			s.Concurrency = d.Concurrency
		}
		if d.Retries > 0 {
			s.Retries = d.Retries
		}
		if d.Dir != "" {
			s.Dir = d.Dir
		}
		s.LogLines = d.LogLines
	}
	if c == nil {
		return s
	}
	flags := c.Root().PersistentFlags()
	if flags.Changed("concurrency") {
		if v, err := flags.GetInt("concurrency"); err == nil && v > 0 {
			s.Concurrency = v
		}
	}
	if flags.Changed("download-dir") {
		if v, err := flags.GetString("download-dir"); err == nil && v != "" {
			s.Dir = v
		}
	}
	if flags.Changed("log-lines") {
		if v, err := flags.GetInt("log-lines"); err == nil && v > 0 {
			s.LogLines = v
		}
	}
	s.NoTUI, _ = flags.GetBool("no-tui")
	return s
}

// buildDownloads parses the top-level <downloads> settings element.
func buildDownloads(n *xnode) (*Downloads, error) {
	if err := checkAttrs(n, "concurrency", "retries", "dir", "log_lines"); err != nil {
		return nil, err
	}
	if len(n.children()) > 0 {
		return nil, fmt.Errorf("<downloads>: settings element takes no children (a per-command hand-off is <download>)")
	}
	d := &Downloads{Dir: strings.TrimSpace(n.attr("dir"))}
	for _, f := range []struct {
		attr string
		dst  *int
	}{
		{"concurrency", &d.Concurrency},
		{"retries", &d.Retries},
		{"log_lines", &d.LogLines},
	} {
		raw := strings.TrimSpace(n.attr(f.attr))
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("<downloads>: %s=%q must be an integer", f.attr, raw)
		}
		*f.dst = v
	}
	return d, nil
}

// buildDownload parses one <download> hand-off on a leaf.
func buildDownload(n *xnode) (Download, error) {
	if err := checkAttrs(n, "over", "when"); err != nil {
		return Download{}, err
	}
	d := Download{Over: strings.TrimSpace(n.attr("over")), When: n.attr("when")}
	for _, child := range n.children() {
		switch child.name {
		case "url":
			s, err := compileTextElem(child)
			if err != nil {
				return Download{}, err
			}
			d.URL = strings.TrimSpace(s)
		case "to":
			s, err := compileTextElem(child)
			if err != nil {
				return Download{}, err
			}
			d.To = strings.TrimSpace(s)
		case "header", "cookie":
			h, err := buildHeader(child, "")
			if err != nil {
				return Download{}, err
			}
			d.append(child.name, h)
		case "if":
			if err := checkAttrs(child, "test"); err != nil {
				return Download{}, err
			}
			test := child.attr("test")
			for _, inner := range child.children() {
				if inner.name != "header" && inner.name != "cookie" {
					return Download{}, fmt.Errorf("<download><if>: only <header> and <cookie> children are supported, got <%s>", inner.name)
				}
				h, err := buildHeader(inner, test)
				if err != nil {
					return Download{}, err
				}
				d.append(inner.name, h)
			}
		default:
			return Download{}, fmt.Errorf("<download>: unexpected child element <%s>", child.name)
		}
	}
	return d, nil
}

func (d *Download) append(kind string, h Header) {
	if kind == "cookie" {
		d.Cookies = append(d.Cookies, h)
		return
	}
	d.Headers = append(d.Headers, h)
}

// validateDownloadSettings checks the top-level <downloads> element. A nil
// settings block is fine — the defaults apply.
func validateDownloadSettings(d *Downloads) error {
	if d == nil {
		return nil
	}
	for _, f := range []struct {
		name string
		val  int
		min  int
	}{
		{"concurrency", d.Concurrency, 1},
		{"retries", d.Retries, 0},
		{"log_lines", d.LogLines, 0},
	} {
		if f.val != 0 && f.val < f.min {
			return fmt.Errorf("<downloads>: %s=%d must be >= %d", f.name, f.val, f.min)
		}
	}
	return nil
}

// validateDownloads checks a command's <download> declarations: they are the
// leaf's action, so they cannot sit on a group node, and their output is file
// paths rather than records for a formatter to shape.
func validateDownloads(c *Command, where string) error {
	if len(c.Downloads) == 0 {
		return nil
	}
	if len(c.Commands) > 0 {
		return fmt.Errorf("%s: <download> is only allowed on leaves (nodes with no subcommands)", where)
	}
	if c.Fields != nil || c.Format.Defined() {
		return fmt.Errorf("%s: <download> writes files rather than records, so <fields>/<format> cannot shape it", where)
	}
	for i := range c.Downloads {
		if strings.TrimSpace(c.Downloads[i].URL) == "" {
			return fmt.Errorf("%s.downloads[%d]: <download> requires a <url>", where, i)
		}
	}
	return nil
}

// planDownloads renders every declaration into concrete specs. It is the
// hand-off point: after this the queue owns the work, and nothing downstream
// needs the config again.
func planDownloads(dls []Download, data map[string]any, dir string) ([]downloadSpec, error) {
	var out []downloadSpec
	for i := range dls {
		d := &dls[i]
		if d.When != "" {
			verdict, err := renderString(d.When, data)
			if err != nil {
				return nil, fmt.Errorf("download[%d]: render when: %w", i, err)
			}
			logVerbose("download[%d]: when %q => %q (truthy=%v)", i, d.When, verdict, isTruthy(verdict))
			if !isTruthy(verdict) {
				continue
			}
		}
		records, err := downloadRecords(d, data, i)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			specs, err := planOne(d, rec.ctx, dir, i)
			if err != nil {
				return nil, err
			}
			out = append(out, specs...)
		}
	}
	return out, nil
}

type downloadRecord struct{ ctx map[string]any }

// downloadRecords expands `over=` into one context per record. A path that
// resolves to nothing is a config error, not an empty run: silently downloading
// zero files is exactly how a renamed field goes unnoticed. An empty list, on
// the other hand, legitimately means "nothing matched".
func downloadRecords(d *Download, data map[string]any, idx int) ([]downloadRecord, error) {
	if d.Over == "" {
		return []downloadRecord{{ctx: data}}, nil
	}
	src := lookupPath(data, d.Over)
	if src == nil {
		return nil, fmt.Errorf("download[%d]: over=%q resolved to nothing", idx, d.Over)
	}
	list, ok := src.([]any)
	if !ok {
		return []downloadRecord{{ctx: downloadCtx(data, src)}}, nil
	}
	out := make([]downloadRecord, 0, len(list))
	for _, el := range list {
		out = append(out, downloadRecord{ctx: downloadCtx(data, el)})
	}
	logVerbose("download[%d]: over=%q expanded to %d records", idx, d.Over, len(out))
	return out, nil
}

// downloadCtx promotes a record's keys onto the data context and exposes the
// record itself as .item, so a list of plain URL strings works as well as a
// list of objects.
func downloadCtx(data map[string]any, rec any) map[string]any {
	ctx := make(map[string]any, len(data)+8)
	for k, v := range data {
		ctx[k] = v
	}
	if m, ok := rec.(map[string]any); ok {
		for k, v := range m {
			ctx[k] = v
		}
	}
	ctx["item"] = rec
	return ctx
}

// planOne renders one declaration against one record. A <url> that renders to
// several lines yields several downloads — which is what a <for> loop inside it
// produces — and then <to> names the directory they share.
func planOne(d *Download, ctx map[string]any, dir string, idx int) ([]downloadSpec, error) {
	rawURL, err := renderString(d.URL, ctx)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: render url: %w", idx, err)
	}
	urls := splitLines(rawURL)
	if len(urls) == 0 {
		return nil, fmt.Errorf("download[%d]: <url> rendered empty", idx)
	}
	// Checked here rather than at the socket: a <url> built from a mistyped
	// path renders to the template engine's placeholder, and "unsupported
	// protocol scheme" some seconds later does not point at the typo.
	for _, u := range urls {
		if parsed, err := url.Parse(u); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("download[%d]: <url> rendered %q, which is not an http(s) URL", idx, u)
		}
	}

	to := ""
	if d.To != "" {
		if to, err = renderString(d.To, ctx); err != nil {
			return nil, fmt.Errorf("download[%d]: render to: %w", idx, err)
		}
		to = strings.TrimSpace(to)
	}

	headers, err := renderHeaders(d.Headers, ctx)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: %w", idx, err)
	}
	cookies, err := renderHeaders(d.Cookies, ctx)
	if err != nil {
		return nil, fmt.Errorf("download[%d]: %w", idx, err)
	}
	if cookie := joinCookies(headers, cookies); cookie != "" {
		headers = append(withoutHeader(headers, "Cookie"), renderedHeader{Name: "Cookie", Value: cookie})
	}

	out := make([]downloadSpec, 0, len(urls))
	for _, u := range urls {
		dest, isDir := downloadDest(dir, to, len(urls) > 1)
		out = append(out, downloadSpec{URL: u, Dest: dest, DestIsDir: isDir, Headers: headers})
	}
	return out, nil
}

// downloadDest resolves where a file lands. An empty <to> means "the download
// directory, named by the server"; a <to> ending in "/" or naming an existing
// directory means the same with an explicit directory; anything else is the
// exact file path. Several URLs sharing one <to> always treat it as a
// directory — one name cannot serve them all.
func downloadDest(dir, to string, multi bool) (string, bool) {
	if to == "" {
		return dir, true
	}
	isDir := multi || strings.HasSuffix(to, "/") || strings.HasSuffix(to, string(os.PathSeparator))
	full := to
	if !filepath.IsAbs(to) {
		full = filepath.Join(dir, to)
	}
	if !isDir {
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			isDir = true
		}
	}
	return filepath.Clean(full), isDir
}

// joinCookies folds <cookie> entries (and any Cookie header the author wrote by
// hand) into one Cookie header value.
func joinCookies(headers, cookies []renderedHeader) string {
	var parts []string
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Cookie") && strings.TrimSpace(h.Value) != "" {
			parts = append(parts, strings.TrimSpace(h.Value))
		}
	}
	for _, c := range cookies {
		if strings.TrimSpace(c.Value) == "" {
			continue
		}
		parts = append(parts, c.Name+"="+strings.TrimSpace(c.Value))
	}
	return strings.Join(parts, "; ")
}

func withoutHeader(headers []renderedHeader, name string) []renderedHeader {
	out := headers[:0:0]
	for _, h := range headers {
		if !strings.EqualFold(h.Name, name) {
			out = append(out, h)
		}
	}
	return out
}

// splitLines returns the non-empty trimmed lines of s. A URL cannot contain a
// newline, so this is an unambiguous way to let one <url> yield many.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
