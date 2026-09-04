package main

import (
	"bytes"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wow-look-at-my/tml"
)

// A <tml> leaf under --watch runs as a real Bubble Tea program rather than
// through the repaint loop in watch.go. Two reasons, and the first one decides
// it: TML takes a program down that paints for longer than tml.DriveGrace with
// nothing able to drive it, and only tml.NewProgram builds one the library can
// reach. The second is that a terminal program owns the alternate screen, the
// resize and the key handling that a dashboard wants anyway.
//
// A tick is one whole run of the leaf, exactly as a watch frame is: the steps,
// the request and the component render, captured into a buffer that becomes the
// frame. Nothing is cached between ticks.

// tmlTickMsg asks the model to run the leaf again.
type tmlTickMsg time.Time

type tmlModel struct {
	every         time.Duration
	body          func() error
	frame         string
	width, height int
	code          int
}

// runTMLProgram paints body's output every interval until the user quits. A
// zero interval draws one frame and waits, which is a dashboard of a thing that
// does not change on its own.
func runTMLProgram(every time.Duration, body func() error) error {
	_, width, height := stdoutSize()
	m := &tmlModel{every: every, body: body, width: width, height: height}
	if m.width <= 0 {
		m.width, m.height = 80, 24
	}
	program, err := tml.NewProgram(m)
	if err != nil {
		return err
	}
	final, err := program.Run()
	if err != nil {
		return err
	}
	if done, ok := final.(*tmlModel); ok && done.code != 0 {
		exitCode = done.code
	}
	return nil
}

func (m *tmlModel) Init() tea.Cmd { return m.refresh() }

func (m *tmlModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, m.refresh()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			m.code = interruptExitCode
			return m, tea.Quit
		case "q", "esc":
			return m, tea.Quit
		case "r":
			return m, m.refresh()
		}
	case tml.RepaintMsg:
		return m, m.refresh()
	case tmlTickMsg:
		return m, m.refresh()
	}
	return m, nil
}

// View draws the last frame on the alternate screen, which is what keeps a
// repeating dashboard from scrolling the user's shell history away.
func (m *tmlModel) View() tea.View {
	view := tea.NewView(m.frame)
	view.AltScreen = true
	return view
}

// refresh runs the leaf into a buffer and keeps the result as the frame. The
// run happens here rather than in a command, because the leaf writes through
// the package's own output channels and two runs cannot hold them at once.
func (m *tmlModel) refresh() tea.Cmd {
	var buf bytes.Buffer
	prev := ttyOverride
	ttyOverride = &termSize{width: m.width, height: m.height}
	err := captureInto(&buf, m.body)
	ttyOverride = prev
	if err != nil {
		buf.WriteString("\nerror: " + err.Error() + "\n")
	}
	m.frame = buf.String()
	if m.every <= 0 {
		return nil
	}
	return tea.Tick(m.every, func(t time.Time) tea.Msg { return tmlTickMsg(t) })
}
