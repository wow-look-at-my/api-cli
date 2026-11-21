// The tui subcommand is an interactive explorer: the command tree, a form for the selected command's args and flags.

package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/tml"
)

//go:embed ui/browse
var browseUI embed.FS

func loadBrowseView() (*tml.View, error) {
	sub, err := fs.Sub(browseUI, "ui/browse")
	if err != nil {
		return nil, err
	}
	return tml.Load(sub, "Browse.tml", tml.Options{Dark: true})
}

// browseOutput is what one run printed: the text a terminal shows, the body before any formatting, and the exit code.
type browseOutput struct {
	text string
	raw  string
	code int
}

// browseRunMsg carries the result of one run back to the model.
type browseRunMsg struct {
	id   int
	line string
	out  browseOutput
	took time.Duration
}

// browseTickMsg asks for the next run of a command that declares watch=.
type browseTickMsg struct{ id int }

// browseHost is the part of a tml.Program that the browser uses. A test supplies its own.
type browseHost interface {
	Quit()
	Go(work func() tml.Msg)
}

type browseModel struct {
	view    *tml.View
	name    string
	entries []*browseEntry
	byPath  map[string]*browseEntry
	cur     *browseEntry

	// filter narrows the tree. filtering is true while the user types it.
	filter    string
	filtering bool

	// frames is the result history: the last one shows, and going back pops it.
	frames       []*resultFrame
	rowFilter    string
	rowFiltering bool

	status      string
	statusStyle string
	running     bool
	runID       int
	launched    *pendingRun
	confirming  bool
	pending     *pendingRun

	menu *browseMenu
	help bool

	// memory holds the last value typed for each input name, which prefills the same name in every other command.
	memory map[string]string
	// statePath is the file the session saves to, or empty for a session that saves nothing.
	statePath string

	host          browseHost
	width, height int
	code          int

	// exec runs one command line and returns what it printed.
	exec func(argv []string, width, height int) browseOutput
	// now times a run. A test fixes it, so a screen holds no wall-clock time.
	now func() time.Time

	// ctx bounds the run in progress, and cancel stops it. The program's goroutine sets both before the work starts.
	ctx       context.Context
	cancel    context.CancelFunc
	cancelled bool

	// live is what the run in progress has printed so far.
	live      *liveBuffer
	liveEvery time.Duration
}

// liveRefresh is how often the result pane shows more of a run in progress.
const liveRefresh = 250 * time.Millisecond

// browseLiveMsg asks for a repaint of a run in progress.
type browseLiveMsg struct{ id int }

// liveBuffer is a buffer that the run's goroutine writes and the program's goroutine reads.
type liveBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *liveBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *liveBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// liveTick repaints a run in progress and asks for the next repaint, until the run ends.
func (m *browseModel) liveTick(msg browseLiveMsg) bool {
	if msg.id != m.runID || !m.running {
		return false
	}
	every := m.liveEvery
	m.host.Go(func() tml.Msg {
		time.Sleep(every)
		return browseLiveMsg{id: msg.id}
	})
	return true
}

// begin opens the context for a new run, and a fresh buffer for what it prints on its way.
func (m *browseModel) begin() {
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.cancelled = false
	m.live = &liveBuffer{}
	if m.liveEvery > 0 {
		id := m.runID + 1
		every := m.liveEvery
		m.host.Go(func() tml.Msg {
			time.Sleep(every)
			return browseLiveMsg{id: id}
		})
	}
}

// runContext is the context of the run in progress.
func (m *browseModel) runContext() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// end releases the context of a finished run.
func (m *browseModel) end() {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
}

// stop cancels the run in progress. Its result still arrives, with whatever it printed before it stopped.
func (m *browseModel) stop() {
	if !m.running || m.cancel == nil {
		return
	}
	m.cancelled = true
	m.cancel()
	m.setStatus("cancelling…", "dim")
}

// pendingRun is a run on its way: what it runs, and whether its result goes on top of the history or replaces the top.
type pendingRun struct {
	entry *browseEntry
	argv  []string
	push  bool
	// keep is the frame whose selection a refresh carries over.
	keep *resultFrame
	// batch is set for a run of one link over several marked rows, with the question its confirmation asks.
	batch  []batchItem
	prompt string
}

// tuiCommand is the built-in `tui` subcommand.
func tuiCommand(cfg *Config) *cobra.Command {
	return &cobra.Command{
		Use:          "tui",
		Short:        "Explore the commands interactively: pick one, fill in its args and flags, run it",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			return runBrowse(cfg)
		},
	}
}

func runBrowse(cfg *Config) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("tui needs a terminal on stdin and stdout")
	}
	m, err := newBrowseModel(cfg)
	if err != nil {
		return err
	}
	if path, perr := statePath(cfg); perr == nil {
		m.attachState(path)
	} else {
		m.setStatus("state is not saved: "+perr.Error(), "bad")
	}
	program, err := tml.NewProgram(m.view, m)
	if err != nil {
		return err
	}
	err = program.Run()
	m.save()
	if errors.Is(err, tml.ErrInterrupted) {
		m.code, err = interruptExitCode, nil
	}
	if err != nil {
		return err
	}
	exitCode = m.code
	return nil
}

func newBrowseModel(cfg *Config) (*browseModel, error) {
	view, err := loadBrowseView()
	if err != nil {
		return nil, fmt.Errorf("browse display: %w", err)
	}
	entries := flattenBrowse(cfg)
	shown := visibleEntries(entries, "")
	if len(shown) == 0 {
		return nil, fmt.Errorf("the config declares no commands to browse")
	}
	m := &browseModel{
		view:    view,
		name:    cfg.Name,
		entries: entries,
		byPath:  map[string]*browseEntry{},
		cur:     shown[0],
		memory:  map[string]string{},
		width:   100,
		height:  30,
		now:     time.Now,
	}
	m.exec = func(argv []string, w, h int) browseOutput {
		return browseExecLive(m.runContext(), m.live, cfg, argv, w, h)
	}
	m.liveEvery = liveRefresh
	for _, e := range entries {
		m.byPath[e.pathString()] = e
	}
	// A large tree opens with its top-level groups shut, so the first screen is a table of contents.
	if len(entries) > collapseAbove {
		for _, e := range entries {
			e.collapsed = e.depth == 0 && e.group()
		}
	}
	m.frame()
	m.focus("tree")
	return m, nil
}

// collapseAbove is the tree size past which the browser opens with its top-level groups shut.
const collapseAbove = 40

// focus moves the keyboard to id.
func (m *browseModel) focus(id string) {
	if m.view.UI().Focus(id) {
		return
	}
	m.frame()
	m.view.UI().Focus(id)
}

// browseExec is browseExecCtx with no way to cancel.
func browseExec(cfg *Config, argv []string, width, height int) browseOutput {
	return browseExecCtx(context.Background(), cfg, argv, width, height)
}

// browseExecCtx is browseExecLive with no live copy.
func browseExecCtx(ctx context.Context, cfg *Config, argv []string, width, height int) browseOutput {
	return browseExecLive(ctx, nil, cfg, argv, width, height)
}

// browseExecLive runs argv in this process, exactly as the command line would, and captures everything it prints.
// The browser is the confirmation, so the run gets --yes. Stdin is empty, because the terminal belongs to the browser.
// raw is the body before any formatting, which the data view reads. A cancel of ctx stops the commands and requests.
// A copy of the output goes to live while the run goes on, and a nil live copies nothing.
func browseExecLive(ctx context.Context, live io.Writer, cfg *Config, argv []string, width, height int) browseOutput {
	var captured bytes.Buffer
	var buf io.Writer = &captured
	if live != nil {
		buf = io.MultiWriter(&captured, live)
	}
	var body *string
	prevIn, prevTTY, prevCode, prevTap, prevCtx := execStdin, ttyOverride, exitCode, bodyTap, runCtx
	execStdin = strings.NewReader("")
	ttyOverride = &termSize{width: width, height: height}
	exitCode = 0
	bodyTap = func(s string) { body = &s }
	runCtx = ctx
	defer func() {
		execStdin, ttyOverride, exitCode, bodyTap, runCtx = prevIn, prevTTY, prevCode, prevTap, prevCtx
	}()

	root := newRoot(cfg)
	root.SetArgs(append([]string{"--yes", "--watch=off", "--no-tui"}, argv...))
	root.SetOut(buf)
	root.SetErr(buf)
	err := captureInto(buf, root.Execute)
	code := exitCode
	if err != nil && code == 0 {
		code = 1
	}
	out := browseOutput{text: captured.String(), raw: captured.String(), code: code}
	if body != nil {
		out.raw = *body
	}
	return out
}

// Update takes one message from the program.
func (m *browseModel) Update(p *tml.Program, msg tml.Msg) bool { return m.update(p, msg) }

func (m *browseModel) update(host browseHost, msg tml.Msg) bool {
	m.host = host
	switch msg := msg.(type) {
	case tml.Resize:
		m.width, m.height = msg.Width, msg.Height
	case browseRunMsg:
		m.finish(msg)
		return true
	case browseTickMsg:
		m.tick(msg)
		return true
	case browseBatchMsg:
		m.finishBatch(msg)
		return true
	case browseLiveMsg:
		return m.liveTick(msg)
	case browseSuggestMsg:
		m.suggested(msg)
		return true
	case tml.Key:
		return m.key(msg)
	case tml.Event:
		m.event(msg)
		return true
	}
	return false
}

// event handles what the focus ring made of a key or a click. A click on a row of the tree, the result or a menu picks that row.
func (m *browseModel) event(event tml.Event) {
	switch event.Kind {
	case tml.Activated:
		switch {
		case event.ID == "tree" && event.Y >= 0:
			m.pickVisible(m.treeStart() + event.Y)
			return
		case event.ID == "result" && event.Y >= 0:
			m.clickRow(event.Y - tableHeaderRows)
			return
		case event.ID == "output" && event.Y >= 0:
			return
		case event.ID == "menu" && event.Y >= 0 && m.menu != nil:
			m.menu.click(m.menuStart() + event.Y)
			return
		}
		m.act(event.Action)
	case tml.Scrolled:
		m.scroll(event.ID, event.Delta)
	case tml.Changed:
		if f := m.field(event.ID); f != nil {
			f.value = event.Value
		}
	}
}

func (m *browseModel) act(action string) {
	kind, arg, _ := strings.Cut(action, ":")
	switch kind {
	case "run":
		m.start(false)
	case "toggle":
		if f := m.field(arg); f != nil {
			f.checked = !f.checked
		}
	case "confirm":
		m.confirming = false
		if m.pending != nil && len(m.pending.batch) > 0 {
			m.launchBatch(m.pending)
			return
		}
		m.launch(m.pending)
	case "cancel":
		m.confirming = false
		m.setStatus("cancelled", "dim")
	}
}

func (m *browseModel) setStatus(text, style string) { m.status, m.statusStyle = text, style }

func (m *browseModel) field(id string) *browseField {
	for i := range m.cur.fields {
		if m.cur.fields[i].id == id {
			return &m.cur.fields[i]
		}
	}
	return nil
}

// visible is the tree rows as drawn.
func (m *browseModel) visible() []*browseEntry { return visibleEntries(m.entries, m.filter) }

func (m *browseModel) pickVisible(i int) {
	vis := m.visible()
	if len(vis) == 0 {
		return
	}
	m.pick(vis[max(0, min(i, len(vis)-1))])
}

// pick selects an entry. Its empty inputs take the values typed for the same names elsewhere, and an autorun command runs.
func (m *browseModel) pick(e *browseEntry) {
	if e == nil || e == m.cur {
		return
	}
	m.cur = e
	m.prefill(e)
	if e.hints.Autorun && !m.running && len(e.missing()) == 0 {
		if top := m.top(); top == nil || top.entry != e {
			m.start(false)
		}
	}
}

// prefill fills an entry's empty args and required flags from the shared memory.
func (m *browseModel) prefill(e *browseEntry) {
	for i := range e.fields {
		f := &e.fields[i]
		if f.isBool() || f.value != "" || (f.arg == nil && !f.required) {
			continue
		}
		if v, ok := m.memory[f.name()]; ok {
			f.value = v
		}
	}
}

// remember stores what an entry's form holds, by input name.
func (m *browseModel) remember(e *browseEntry) {
	for k, v := range e.inputs() {
		m.memory[k] = v
	}
}

// reveal makes e a visible tree row: it opens the groups above it, and clears a filter that hides it.
func (m *browseModel) reveal(e *browseEntry) {
	for p := e.parent; p != nil; p = p.parent {
		p.collapsed = false
	}
	if !slices.Contains(m.visible(), e) {
		m.filter = ""
	}
}

// start validates the form and runs it, through the confirm popup when the command asks for one.
// A run from the form replaces the result on top of the history. A link pushes its result, so going back returns to the record it came from.
func (m *browseModel) start(push bool) {
	e := m.cur
	if m.running || !e.runnable() {
		return
	}
	argv, err := e.argv()
	if err != nil {
		m.setStatus(err.Error(), "bad")
		if miss := e.missing(); len(miss) > 0 {
			m.focus(miss[0].id)
		}
		return
	}
	run := &pendingRun{entry: e, argv: argv, push: push || m.top() == nil}
	if e.confirm != "" {
		m.pending, m.confirming = run, true
		m.focus("confirm-no")
		return
	}
	m.launch(run)
}

// launch runs argv off the program's goroutine. The program moves the spinner while it runs.
func (m *browseModel) launch(run *pendingRun) {
	if run == nil {
		return
	}
	m.running = true
	m.begin()
	m.runID++
	m.launched = run
	m.remember(run.entry)
	m.save()
	line := commandLine(m.name, run.argv)
	m.setStatus("running "+line, "dim")
	exec, width, height, id, argv, now := m.exec, m.outputWidth(), m.height, m.runID, run.argv, m.now
	began := now()
	m.host.Go(func() tml.Msg {
		out := exec(argv, width, height)
		return browseRunMsg{id: id, line: line, out: out, took: now().Sub(began)}
	})
}

// outputWidth is the width a run formats for: the output pane inside its border.
func (m *browseModel) outputWidth() int { return max(20, m.width-4) }

// frame renders one screen as text. A render failure is shown rather than swallowed.
func (m *browseModel) frame() string {
	out, err := m.view.Render(m.Props(), m.width, m.height)
	if err != nil {
		return "browse: " + err.Error()
	}
	return out
}
