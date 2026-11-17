// A <tml> leaf under a watch runs as a tml program rather than through the repaint loop in watch.go.

package main

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"time"

	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

//go:embed ui/frame
var frameUI embed.FS

// tmlTickMsg asks the model to run the leaf again.
type tmlTickMsg struct{}

type tmlModel struct {
	every         time.Duration
	body          func() error
	frame         string
	width, height int
	code          int
	// cancel stops the tick that waits.
	cancel func()
}

// runTMLProgram paints body's output every interval until the user quits.
func runTMLProgram(every time.Duration, body func() error) error {
	return runTMLWith(every, body, tml.ProgramOptions{NoMouse: true})
}

func runTMLWith(every time.Duration, body func() error, opts tml.ProgramOptions) error {
	sub, err := fs.Sub(frameUI, "ui/frame")
	if err != nil {
		return err
	}
	view, err := tml.Load(sub, "Frame.tml", tml.Options{Dark: true})
	if err != nil {
		return err
	}
	m := &tmlModel{every: every, body: body}
	program, err := tml.NewProgram(view, m, opts)
	if err != nil {
		return err
	}
	err = program.Run()
	if m.cancel != nil {
		m.cancel()
	}
	if errors.Is(err, tml.ErrInterrupted) {
		m.code, err = interruptExitCode, nil
	}
	if err != nil {
		return err
	}
	if m.code != 0 {
		exitCode = m.code
	}
	return nil
}

// Props is the last frame that the leaf printed.
func (m *tmlModel) Props() tml.Props { return tml.Props{"frame": sema.StringValue(m.frame)} }

// Update takes one message from the program. A resize runs the leaf again at the new size.
func (m *tmlModel) Update(p *tml.Program, msg tml.Msg) bool {
	switch msg := msg.(type) {
	case tml.Resize:
		m.width, m.height = msg.Width, msg.Height
		m.refresh(p)
		return true
	case tml.Key:
		switch msg.String() {
		case "ctrl+c":
			m.code = interruptExitCode
			p.Quit()
		case "q", "esc":
			p.Quit()
		case "r":
			m.refresh(p)
		default:
			return false
		}
		return true
	case tml.RepaintMsg, tmlTickMsg:
		m.refresh(p)
		return true
	}
	return false
}

// refresh runs the leaf into a buffer, keeps the result as the frame, and sets
// the next tick. The run stays on the program's goroutine, because the leaf
// writes through the package's output channels and the runs cannot share them.
func (m *tmlModel) refresh(p *tml.Program) {
	var buf bytes.Buffer
	width, height := m.width, m.height
	if width <= 0 || height <= 0 {
		width, height = 80, 24
	}
	prev := ttyOverride
	ttyOverride = &termSize{width: width, height: height}
	err := captureInto(&buf, m.body)
	ttyOverride = prev
	if err != nil {
		buf.WriteString("\nerror: " + err.Error() + "\n")
	}
	m.frame = buf.String()
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = p.After(m.every, tmlTickMsg{})
}
