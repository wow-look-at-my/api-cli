package main

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/api-cli/fields"

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
	// RetriesSet distinguishes retries="0" -- a config that wants a failure
	// reported immediately -- from an absent attribute, which takes the default.
	RetriesSet bool `json:"retriesSet,omitempty"`
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
	Over      string   `json:"over,omitempty"`
	When      string   `json:"when,omitempty"`
	URL       string   `json:"url"`
	To        string   `json:"to,omitempty"`
	Headers   []Header `json:"headers,omitempty"`
	Cookies   []Header `json:"cookies,omitempty"`
	Hash      string   `json:"hash,omitempty"`      // template for the expected digest
	HashAlgo  string   `json:"hashAlgo,omitempty"`  // md5 | sha1 | sha256 (default) | sha512
	Transport string   `json:"transport,omitempty"` // registry name; empty means the config default
}

// hashAlgos maps a <hash algo=> to the length of its digest in hex characters.
// md5 and sha1 are here because real manifests still publish them, not because
// they are a good choice for anything but catching a mangled transfer.
var hashAlgos = map[string]int{
	"md5":    32,
	"sha1":   40,
	"sha256": 64,
	"sha512": 128,
}

const defaultHashAlgo = "sha256"

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
		if d.RetriesSet || d.Retries > 0 {
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
	if len(n.Children()) > 0 {
		return nil, fmt.Errorf("<downloads>: settings element takes no children (a per-command hand-off is <download>)")
	}
	d := &Downloads{Dir: strings.TrimSpace(n.Attr("dir"))}
	for _, f := range []struct {
		attr string
		dst  *int
	}{
		{"concurrency", &d.Concurrency},
		{"retries", &d.Retries},
		{"log_lines", &d.LogLines},
	} {
		raw := strings.TrimSpace(n.Attr(f.attr))
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("<downloads>: %s=%q must be an integer", f.attr, raw)
		}
		*f.dst = v
	}
	d.RetriesSet = n.HasAttr("retries")
	return d, nil
}

// buildDownload parses one <download> hand-off on a leaf.
func buildDownload(n *xnode) (Download, error) {
	if err := checkAttrs(n, "over", "when", "transport"); err != nil {
		return Download{}, err
	}
	d := Download{
		Over:      strings.TrimSpace(n.Attr("over")),
		When:      n.Attr("when"),
		Transport: strings.TrimSpace(n.Attr("transport")),
	}
	for _, child := range n.Children() {
		switch child.Name() {
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
		case "hash":
			// compileContent, not compileTextElem: the latter rejects every
			// attribute, and <hash> carries algo=.
			if err := checkAttrs(child, "algo"); err != nil {
				return Download{}, err
			}
			s, err := compileContent(child)
			if err != nil {
				return Download{}, err
			}
			d.Hash = strings.TrimSpace(s)
			d.HashAlgo = strings.ToLower(strings.TrimSpace(child.Attr("algo")))
			if d.HashAlgo == "" {
				d.HashAlgo = defaultHashAlgo
			}
		case "header", "cookie":
			h, err := buildHeader(child, "")
			if err != nil {
				return Download{}, err
			}
			d.append(child.Name(), h)
		case "if":
			if err := checkAttrs(child, "test"); err != nil {
				return Download{}, err
			}
			test := child.Attr("test")
			for _, inner := range child.Children() {
				if inner.Name() != "header" && inner.Name() != "cookie" {
					return Download{}, fmt.Errorf("<download><if>: only <header> and <cookie> children are supported, got <%s>", inner.Name())
				}
				h, err := buildHeader(inner, test)
				if err != nil {
					return Download{}, err
				}
				d.append(inner.Name(), h)
			}
		default:
			return Download{}, fmt.Errorf("<download>: unexpected child element <%s>", child.Name())
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
// node's action, so they need a node that runs, and their output is file paths
// rather than records for a formatter to shape.
func validateDownloads(c *Command, where string, transports map[string]*Transport) error {
	if len(c.Downloads) == 0 {
		return nil
	}
	if !c.executes() {
		return fmt.Errorf("%s: <download> needs a node that runs (a leaf, or a parent with runnable=)", where)
	}
	if len(c.Fields) > 0 || c.Format.Defined() {
		return fmt.Errorf("%s: <download> writes files rather than records, so <fields>/<format> cannot shape it", where)
	}
	for i := range c.Downloads {
		d := &c.Downloads[i]
		if strings.TrimSpace(d.URL) == "" {
			return fmt.Errorf("%s.downloads[%d]: <download> requires a <url>", where, i)
		}
		if d.Hash != "" {
			if _, ok := hashAlgos[d.HashAlgo]; !ok {
				return fmt.Errorf("%s.downloads[%d]: <hash algo=%q> must be one of %s", where, i, d.HashAlgo, knownHashAlgos())
			}
		}
		name := strings.TrimSpace(d.Transport)
		if name == "" || name == builtinTransportName {
			continue
		}
		if _, ok := transports[name]; !ok {
			return fmt.Errorf("%s.downloads[%d]: references unknown transport %q", where, i, name)
		}
	}
	return nil
}

func knownHashAlgos() string {
	names := make([]string, 0, len(hashAlgos))
	for name := range hashAlgos {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

// planDownloads renders every declaration into concrete specs. It is the
// hand-off point: after this the queue owns the work, and nothing downstream
// needs the config again.
func planDownloads(dls []Download, data map[string]any, dir string) ([]downloadSpec, error) {
	var out []downloadSpec
	claimed := map[string]string{}
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
		for _, ctx := range records {
			specs, err := planOne(d, ctx, dir, i)
			if err != nil {
				return nil, err
			}
			for _, spec := range specs {
				// Two records rendering one file name is a <to> that forgot to
				// vary — caught here rather than after N transfers have
				// overwritten each other into one file.
				if !spec.DestIsDir {
					if prev, dup := claimed[spec.Dest]; dup {
						return nil, fmt.Errorf("download[%d]: %s and %s would both write %s; give <to> something that varies per record",
							i, prev, spec.URL, spec.Dest)
					}
					claimed[spec.Dest] = spec.URL
				}
				out = append(out, spec)
			}
		}
	}
	return out, nil
}

// downloadRecords expands `over=` into one render context per record. A path
// that resolves to nothing is a config error, not an empty run: silently
// downloading zero files is exactly how a renamed field goes unnoticed. An empty
// list, on the other hand, legitimately means "nothing matched".
func downloadRecords(d *Download, data map[string]any, idx int) ([]map[string]any, error) {
	if d.Over == "" {
		return []map[string]any{data}, nil
	}
	src, ok := fields.Lookup(data, d.Over)
	if !ok || src == nil {
		return nil, fmt.Errorf("download[%d]: over=%q resolved to nothing", idx, d.Over)
	}
	list, ok := src.([]any)
	if !ok {
		return []map[string]any{downloadCtx(data, src)}, nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, el := range list {
		out = append(out, downloadCtx(data, el))
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

	digest, err := renderHash(d, ctx, idx)
	if err != nil {
		return nil, err
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
		// Resolved per URL: the program's argv sees .request.url, so each file
		// gets its own rendered command.
		transport, err := prepareDownloadTransport(d.Transport, u, headers, ctx)
		if err != nil {
			return nil, fmt.Errorf("download[%d]: %w", idx, err)
		}
		out = append(out, downloadSpec{
			URL: u, Dest: dest, DestIsDir: isDir, Headers: headers,
			Hash: digest, HashAlgo: d.HashAlgo, Transport: transport,
		})
	}
	return out, nil
}

// renderHash renders the expected digest for one record and normalizes it.
//
// An empty render means this record carries no digest — which is how a <hash>
// body wrapped in an <if test=> opts one record of an `over=` list out. Anything
// else must be a digest of the right shape: a manifest field that got renamed
// renders as the template engine's placeholder, and silently not verifying is
// the one outcome a verification feature must never have.
func renderHash(d *Download, ctx map[string]any, idx int) (string, error) {
	if d.Hash == "" {
		return "", nil
	}
	raw, err := renderString(d.Hash, ctx)
	if err != nil {
		return "", fmt.Errorf("download[%d]: render hash: %w", idx, err)
	}
	// A digest often arrives as the first field of a `sha256sum` line
	// ("<hex>  <name>"), so read that field rather than the whole line.
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", nil
	}
	digest := strings.ToLower(fields[0])
	width := hashAlgos[d.HashAlgo]
	if _, err := hex.DecodeString(digest); err != nil || len(digest) != width {
		return "", fmt.Errorf("download[%d]: <hash algo=%q> rendered %q, which is not a %d-character hex digest",
			idx, d.HashAlgo, digest, width)
	}
	return digest, nil
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
