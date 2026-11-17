// The tui subcommand is an interactive explorer: the command tree, a form for the selected command's args and flags.

package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/input"
	"github.com/wow-look-at-my/tml/sema"
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

// browseRunMsg carries the result of one run back to the model.
type browseRunMsg struct {
	line   string
	output string
	code   int
}

// browseHost is the part of a tml.Program that the browser uses. A test supplies its own.
type browseHost interface {
	Quit()
	Go(work func() tml.Msg)
}

type browseModel struct {
	view    *tml.View
	name    string
	entries []*browseEntry
	sel     int

	output      []string
	offset      int
	status      string
	statusStyle string
	running     bool
	confirming  bool
	pending     []string

	width, height int
	code          int

	// exec runs one command line and returns what it printed and its exit code.
	exec func(argv []string, width, height int) (string, int)
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
	program, err := tml.NewProgram(m.view, m)
	if err != nil {
		return err
	}
	err = program.Run()
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
	if len(entries) == 0 {
		return nil, fmt.Errorf("the config declares no commands to browse")
	}
	m := &browseModel{
		view:    view,
		name:    cfg.Name,
		entries: entries,
		width:   100,
		height:  30,
		exec:    func(argv []string, w, h int) (string, int) { return browseExec(cfg, argv, w, h) },
	}
	m.frame()
	m.view.UI().Focus("tree")
	return m, nil
}

// browseExec runs argv in this process, exactly as the command line would,
// and captures everything it prints. The browser is the confirmation, so the
// run gets --yes. Stdin is empty, because the terminal belongs to the browser.
func browseExec(cfg *Config, argv []string, width, height int) (string, int) {
	var buf bytes.Buffer
	prevIn, prevTTY, prevCode := execStdin, ttyOverride, exitCode
	execStdin = strings.NewReader("")
	ttyOverride = &termSize{width: width, height: height}
	exitCode = 0
	defer func() { execStdin, ttyOverride, exitCode = prevIn, prevTTY, prevCode }()

	root := newRoot(cfg)
	root.SetArgs(append([]string{"--yes", "--watch=off", "--no-tui"}, argv...))
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := captureInto(&buf, root.Execute)
	code := exitCode
	if err != nil && code == 0 {
		code = 1
	}
	return buf.String(), code
}

func (m *browseModel) entry() *browseEntry { return m.entries[m.sel] }

// Update takes one message from the program.
func (m *browseModel) Update(p *tml.Program, msg tml.Msg) bool { return m.update(p, msg) }

func (m *browseModel) update(host browseHost, msg tml.Msg) bool {
	switch msg := msg.(type) {
	case tml.Resize:
		m.width, m.height = msg.Width, msg.Height
	case browseRunMsg:
		m.finish(msg)
		return true
	case tml.Key:
		return m.key(host, msg)
	case tml.Event:
		m.event(host, msg)
		return true
	}
	return false
}

// event handles what the focus ring made of a key or a click.
func (m *browseModel) event(host browseHost, event tml.Event) {
	switch event.Kind {
	case tml.Activated:
		if event.ID == "tree" && event.Y >= 0 {
			m.pick(m.treeStart() + event.Y)
			return
		}
		m.act(host, event.Action)
	case tml.Scrolled:
		m.scroll(event.ID, event.Delta)
	case tml.Changed:
		if f := m.field(event.ID); f != nil {
			f.value = event.Value
		}
	}
}

// key handles the keys the browser keeps for itself. Typing goes to the field
// that has focus. Everything else falls through to the focus ring.
func (m *browseModel) key(host browseHost, msg tml.Key) bool {
	focused, _ := m.view.UI().Focused()
	switch msg.String() {
	case "ctrl+c":
		m.code = interruptExitCode
		host.Quit()
		return true
	case "esc":
		if m.confirming {
			m.confirming = false
			m.status, m.statusStyle = "cancelled", "dim"
			return true
		}
		if focused != "tree" {
			m.view.UI().Focus("tree")
			return true
		}
		host.Quit()
		return true
	case "ctrl+r":
		m.start(host)
		return true
	case "pgup":
		m.scroll("output", -m.pageRows())
		return true
	case "pgdown":
		m.scroll("output", m.pageRows())
		return true
	}
	if m.confirming {
		return false
	}
	if focused == "tree" || focused == "" {
		switch msg.String() {
		case "q":
			host.Quit()
			return true
		case "up", "k":
			m.pick(m.sel - 1)
			return true
		case "down", "j":
			m.pick(m.sel + 1)
			return true
		case "enter", "right", "l":
			m.enter(host)
			return true
		}
		return false
	}
	field := m.field(focused)
	if field == nil || field.isBool() {
		return false
	}
	switch msg.String() {
	case "enter":
		m.start(host)
		return true
	case "backspace":
		if _, size := utf8.DecodeLastRuneInString(field.value); size > 0 {
			field.value = field.value[:len(field.value)-size]
		}
		return true
	case "ctrl+u":
		field.value = ""
		return true
	}
	if r := msg.Code; msg.Text != "" && unicode.IsPrint(r) && msg.Mod&^input.Shift == 0 {
		field.value += msg.Text
		return true
	}
	return false
}

// enter moves from the tree into the form, or runs a command that takes no input.
func (m *browseModel) enter(host browseHost) {
	e := m.entry()
	switch {
	case !e.runnable():
	case len(e.fields) == 0:
		m.start(host)
	default:
		m.view.UI().Focus(e.fields[0].id)
	}
}

func (m *browseModel) act(host browseHost, action string) {
	kind, arg, _ := strings.Cut(action, ":")
	switch kind {
	case "run":
		m.start(host)
	case "toggle":
		if f := m.field(arg); f != nil {
			f.checked = !f.checked
		}
	case "confirm":
		m.confirming = false
		m.launch(host, m.pending)
	case "cancel":
		m.confirming = false
		m.status, m.statusStyle = "cancelled", "dim"
	}
}

func (m *browseModel) field(id string) *browseField {
	e := m.entry()
	for i := range e.fields {
		if e.fields[i].id == id {
			return &e.fields[i]
		}
	}
	return nil
}

func (m *browseModel) pick(i int) {
	m.sel = max(0, min(i, len(m.entries)-1))
}

func (m *browseModel) scroll(id string, delta int) {
	switch id {
	case "tree":
		m.pick(m.sel + delta)
	case "output":
		limit := len(m.output)
		if t, ok := m.view.UI().Target("output"); ok {
			limit = t.Scroll.MaxY
		}
		m.offset = max(0, min(m.offset+delta, limit))
	}
}

func (m *browseModel) pageRows() int { return max(1, m.height/3) }

// start validates the form and runs it, through the confirm popup when the
// command asks for one.
func (m *browseModel) start(host browseHost) {
	e := m.entry()
	if m.running || !e.runnable() {
		return
	}
	argv, err := e.argv()
	if err != nil {
		m.status, m.statusStyle = err.Error(), "bad"
		return
	}
	if e.confirm != "" {
		m.pending, m.confirming = argv, true
		m.view.UI().Focus("confirm-no")
		return
	}
	m.launch(host, argv)
}

// launch runs argv off the program's goroutine. The program moves the spinner while it runs.
func (m *browseModel) launch(host browseHost, argv []string) {
	m.running = true
	line := commandLine(m.name, argv)
	m.status, m.statusStyle = "running "+line, "dim"
	exec, width, height := m.exec, m.outputWidth(), m.height
	host.Go(func() tml.Msg {
		out, code := exec(argv, width, height)
		return browseRunMsg{line: line, output: out, code: code}
	})
}

func (m *browseModel) finish(msg browseRunMsg) {
	m.running = false
	m.offset = 0
	m.output = browseLines(msg.output)
	if msg.code == 0 {
		m.status, m.statusStyle = "exit 0  "+msg.line, "ok"
	} else {
		m.status, m.statusStyle = fmt.Sprintf("exit %d  %s", msg.code, msg.line), "bad"
	}
}

// browseLines splits output into the rows the output pane draws. A tab has no
// width of its own in a cell grid, so it becomes spaces.
func browseLines(s string) []string {
	s = strings.ReplaceAll(strings.TrimRight(s, "\n"), "\r", "")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, "\t", "    ")
	}
	return lines
}

// outputWidth is the width a run formats for: the output pane inside its border.
func (m *browseModel) outputWidth() int { return max(20, m.width-4) }

// treeRows is how many tree rows fit in the top half of the screen.
func (m *browseModel) treeRows() int { return max(3, min(len(m.entries), m.height/2-2)) }

// treeStart is the first tree row on screen, chosen so the selection stays visible.
func (m *browseModel) treeStart() int {
	rows := m.treeRows()
	return max(0, min(m.sel-rows/2, len(m.entries)-rows))
}

func (m *browseModel) hint() string {
	focused, _ := m.view.UI().Focused()
	switch {
	case m.confirming:
		return "tab picks, enter chooses, esc cancels"
	case focused == "tree" || focused == "":
		return "up/down pick, enter fills in, ctrl+r runs, q quits"
	default:
		return "type to edit, tab moves, enter runs, esc goes back"
	}
}

// frame renders one screen as text. A render failure is shown rather than swallowed.
func (m *browseModel) frame() string {
	out, err := m.view.Render(m.Props(), m.width, m.height)
	if err != nil {
		return "browse: " + err.Error()
	}
	return out
}

// Props is what the program draws the screen from.
func (m *browseModel) Props() tml.Props {
	e := m.entry()
	start, rows := m.treeStart(), m.treeRows()
	labels := make([]string, 0, rows)
	for _, t := range m.entries[start:min(start+rows, len(m.entries))] {
		labels = append(labels, t.label())
	}
	fields := make([]map[string]sema.Value, 0, len(e.fields))
	for _, f := range e.fields {
		label := f.label
		if f.required {
			label += " *"
		}
		fields = append(fields, map[string]sema.Value{
			"id":      sema.StringValue(f.id),
			"label":   sema.StringValue(label),
			"hint":    sema.StringValue(f.hint),
			"text":    sema.BoolValue(!f.isBool()),
			"value":   sema.StringValue(f.value),
			"cursor":  sema.StringValue(strconv.Itoa(utf8.RuneCountInString(f.value))),
			"checked": sema.BoolValue(f.checked),
		})
	}
	line := ""
	if e.runnable() {
		if argv, err := e.argv(); err == nil {
			line = commandLine(m.name, argv)
		} else {
			line = err.Error()
		}
	}
	style := m.statusStyle
	if style == "" {
		style = "dim"
	}
	return tml.Props{
		"title":       sema.StringValue(m.name),
		"hint":        sema.StringValue(m.hint()),
		"tree":        sema.ListValue(labels),
		"selected":    sema.StringValue(strconv.Itoa(m.sel - start)),
		"path":        sema.StringValue(strings.Join(e.path, " ")),
		"description": sema.StringValue(e.node.Description),
		"runnable":    sema.BoolValue(e.runnable()),
		"group":       sema.BoolValue(!e.runnable()),
		"fields":      sema.RecordListValue(fields),
		"line":        sema.StringValue(line),
		"running":     sema.BoolValue(m.running),
		"status":      sema.StringValue(m.status),
		"statusStyle": sema.StringValue(style),
		"output":      sema.ListValue(m.output),
		"offset":      sema.StringValue(strconv.Itoa(m.offset)),
		"confirming":  sema.BoolValue(m.confirming),
	}
}
