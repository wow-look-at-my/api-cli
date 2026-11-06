// The tui subcommand is an interactive explorer: the command tree, a form for the selected command's args and flags.

package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/tml"
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

type browseTickMsg time.Time

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
	program, err := tml.NewProgram(m)
	if err != nil {
		return err
	}
	if _, err := program.Run(); err != nil {
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

func (m *browseModel) Init() tea.Cmd { return nil }

func (m *browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case browseTickMsg:
		if !m.running {
			return m, nil
		}
		m.view.Advance()
		return m, browseTick()
	case browseRunMsg:
		m.finish(msg)
		return m, nil
	case tea.KeyPressMsg:
		if cmd, handled := m.key(msg); handled {
			return m, cmd
		}
	}

	var cmd tea.Cmd
	for _, event := range m.view.UI().Update(msg) {
		switch event.Kind {
		case tml.Activated:
			if event.ID == "tree" && event.Y >= 0 {
				m.pick(m.treeStart() + event.Y)
				continue
			}
			cmd = m.act(event.Action)
		case tml.Scrolled:
			m.scroll(event.ID, event.Delta)
		}
	}
	if m.code != 0 || cmd != nil {
		return m, cmd
	}
	return m, nil
}

// key handles the keys the browser keeps for itself. Typing goes to the field
// that has focus. Everything else falls through to the focus ring.
func (m *browseModel) key(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	focused, _ := m.view.UI().Focused()
	switch msg.String() {
	case "ctrl+c":
		m.code = interruptExitCode
		return tea.Quit, true
	case "esc":
		if m.confirming {
			m.confirming = false
			m.status, m.statusStyle = "cancelled", "dim"
			return nil, true
		}
		if focused != "tree" {
			m.view.UI().Focus("tree")
			return nil, true
		}
		return tea.Quit, true
	case "ctrl+r":
		return m.start(), true
	case "pgup":
		m.scroll("output", -m.pageRows())
		return nil, true
	case "pgdown":
		m.scroll("output", m.pageRows())
		return nil, true
	}
	if m.confirming {
		return nil, false
	}
	if focused == "tree" || focused == "" {
		switch msg.String() {
		case "q":
			return tea.Quit, true
		case "up", "k":
			m.pick(m.sel - 1)
			return nil, true
		case "down", "j":
			m.pick(m.sel + 1)
			return nil, true
		case "enter", "right", "l":
			return m.enter(), true
		}
		return nil, false
	}
	field := m.field(focused)
	if field == nil || field.isBool() {
		return nil, false
	}
	switch msg.String() {
	case "enter":
		return m.start(), true
	case "backspace":
		if _, size := utf8.DecodeLastRuneInString(field.value); size > 0 {
			field.value = field.value[:len(field.value)-size]
		}
		return nil, true
	case "ctrl+u":
		field.value = ""
		return nil, true
	}
	if r := msg.Code; msg.Text != "" && unicode.IsPrint(r) && msg.Mod&^tea.ModShift == 0 {
		field.value += msg.Text
		return nil, true
	}
	return nil, false
}

// enter moves from the tree into the form, or runs a command that takes no input.
func (m *browseModel) enter() tea.Cmd {
	e := m.entry()
	if !e.runnable() {
		return nil
	}
	if len(e.fields) == 0 {
		return m.start()
	}
	m.view.UI().Focus(e.fields[0].id)
	return nil
}

func (m *browseModel) act(action string) tea.Cmd {
	kind, arg, _ := strings.Cut(action, ":")
	switch kind {
	case "run":
		return m.start()
	case "toggle":
		if f := m.field(arg); f != nil {
			f.checked = !f.checked
		}
	case "confirm":
		m.confirming = false
		return m.launch(m.pending)
	case "cancel":
		m.confirming = false
		m.status, m.statusStyle = "cancelled", "dim"
	}
	return nil
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
func (m *browseModel) start() tea.Cmd {
	e := m.entry()
	if m.running || !e.runnable() {
		return nil
	}
	argv, err := e.argv()
	if err != nil {
		m.status, m.statusStyle = err.Error(), "bad"
		return nil
	}
	if e.confirm != "" {
		m.pending, m.confirming = argv, true
		m.view.UI().Focus("confirm-no")
		return nil
	}
	return m.launch(argv)
}

func (m *browseModel) launch(argv []string) tea.Cmd {
	m.running = true
	m.status, m.statusStyle = "running "+commandLine(m.name, argv), "dim"
	exec, width, height := m.exec, m.outputWidth(), m.height
	line := commandLine(m.name, argv)
	run := func() tea.Msg {
		out, code := exec(argv, width, height)
		return browseRunMsg{line: line, output: out, code: code}
	}
	return tea.Batch(run, browseTick())
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

func browseTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return browseTickMsg(t) })
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

func (m *browseModel) View() tea.View {
	view := tea.NewView(m.frame())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	return view
}

// frame renders one screen. A render failure is shown rather than swallowed.
func (m *browseModel) frame() string {
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
	out, err := m.view.Render(tml.Props{
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
	}, m.width, m.height)
	if err != nil {
		return "browse: " + err.Error()
	}
	return out
}
